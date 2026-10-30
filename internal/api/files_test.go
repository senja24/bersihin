package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/dupes"
	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/snapshot"
)

const (
	errRequired    = "Confirmation required"
	errExpired     = "Confirmation expired"
	errScanExpired = "Scan expired: scan again"
)

func wantErr(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || err.Error() != text {
		t.Fatalf("err = %v, want %q", err, text)
	}
}

// largeScan puts n files of just over 1 MB into Downloads and scans them.
func (e *env) largeScan(t *testing.T, names ...string) (string, []string) {
	t.Helper()
	var paths []string
	for _, n := range names {
		p := filepath.Join(e.home, "Downloads", n)
		put(t, p, 1<<20+1, 48*time.Hour)
		paths = append(paths, p)
	}
	id := rid()
	scan, err := e.svc.Files.ScanLargeFiles(context.Background(), id, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != len(names) {
		t.Fatalf("scan found %d files, want %d", len(scan.Files), len(names))
	}
	return id, paths
}

// S2: Junk.Clean needs a token that matches exactly.
func TestJunkCleanRefusesWithoutAMatchingToken(t *testing.T) {
	e := newEnv(t)
	log := filepath.Join(e.home, "Library", "Logs", "app", "x.log")
	cache := filepath.Join(e.home, "Library", "Caches", "com.foo", "c")
	put(t, log, 100, 48*time.Hour)
	put(t, cache, 100, 48*time.Hour)
	scanID, _ := e.scanJunk(t)
	ids := []string{"mac-logs"}
	run := func(scan string, ids []string, tok string) error {
		_, err := e.svc.Junk.Clean(context.Background(), rid(), scan, ids, tok)
		return err
	}
	mint := func(ids ...string) string {
		tok, err := e.svc.Confirm.Clean(scanID, ids)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	wantErr(t, run(scanID, ids, ""), errRequired)
	wantErr(t, run(scanID, ids, "made-up-token"), errRequired)
	// a token for {mac-logs} is useless for {mac-logs, mac-caches}
	wantErr(t, run(scanID, []string{"mac-logs", "mac-caches"}, mint("mac-logs")), errRequired)
	// expired
	exp := mint("mac-logs")
	e.advance(2*time.Minute + time.Second)
	wantErr(t, run(scanID, ids, exp), errExpired)
	if !fileExists(log) || !fileExists(cache) {
		t.Fatal("something was deleted although every call was refused")
	}

	// used once, never twice
	tok := mint("mac-logs")
	if err := run(scanID, ids, tok); err != nil {
		t.Fatal(err)
	}
	if fileExists(log) {
		t.Fatal("a valid token did not clean")
	}
	put(t, log, 100, 48*time.Hour)
	wantErr(t, run(scanID, ids, tok), errRequired)
	if !fileExists(log) {
		t.Fatal("a reused token cleaned again")
	}
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestJunkCleanTokenIsBoundToTheScan(t *testing.T) {
	e := newEnv(t)
	log := filepath.Join(e.home, "Library", "Logs", "app", "x.log")
	put(t, log, 100, 48*time.Hour)
	scanA, _ := e.scanJunk(t)
	scanB, _ := e.scanJunk(t)
	tok, err := e.svc.Confirm.Clean(scanA, []string{"mac-logs"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Junk.Clean(context.Background(), rid(), scanB, []string{"mac-logs"}, tok)
	wantErr(t, err, errRequired)
	if !fileExists(log) {
		t.Fatal("a token of scan A cleaned for scan B")
	}
}

func TestJunkCleanRefusesUnknownScan(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Library", "Logs", "app", "x.log"), 100, 48*time.Hour)
	_, err := e.svc.Confirm.Clean(rid(), []string{"mac-logs"})
	wantErr(t, err, errScanExpired)
	// a token minted for a real scan, then the scan is forgotten by age
	scanID, _ := e.scanJunk(t)
	e.advance(9 * time.Minute)
	tok, err := e.svc.Confirm.Clean(scanID, []string{"mac-logs"})
	if err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute + time.Second)
	_, err = e.svc.Junk.Clean(context.Background(), rid(), scanID, []string{"mac-logs"}, tok)
	wantErr(t, err, errScanExpired)
}

// S2: the page cannot flip the setting that the UI uses to skip its prompt and
// thereby skip the token: Go enforces it either way.
func TestConfirmBeforeCleanSettingChangesNothingInGo(t *testing.T) {
	e := newEnv(t)
	e.svc.Settings.c.settings.Dir = func() (string, error) { return t.TempDir(), nil }
	off := false
	if _, err := e.svc.Settings.Set(model.SettingsPatch{ConfirmBeforeClean: &off}); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(e.home, "Library", "Logs", "app", "x.log")
	put(t, log, 100, 48*time.Hour)
	scanID, _ := e.scanJunk(t)
	_, err := e.svc.Junk.Clean(context.Background(), rid(), scanID, []string{"mac-logs"}, "")
	wantErr(t, err, errRequired)
	if !fileExists(log) {
		t.Fatal("cleaned without a token")
	}
}

// S2: trashPaths followed by cleanJunk(["trash"]) must be impossible.
func TestTrashThenEmptyTheTrashIsImpossible(t *testing.T) {
	e := newEnv(t)
	inTrash := filepath.Join(e.home, ".Trash", "x.bin")
	put(t, inTrash, 1<<20+1, 48*time.Hour)
	scanID, paths := e.largeScan(t, "a.bin")
	// a path the scan never returned gets no token
	_, err := e.svc.Confirm.Trash(scanID, []string{paths[0], inTrash})
	wantErr(t, err, "Invalid request")
	_, err = e.svc.Confirm.Trash(scanID, []string{inTrash})
	wantErr(t, err, "Invalid request")
	// and a token for the real path does not stretch to it
	tok, err := e.svc.Confirm.Trash(scanID, paths)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Files.TrashPaths(context.Background(), rid(), scanID, []string{paths[0], inTrash}, tok)
	wantErr(t, err, errRequired)
	if len(e.shell.trashed) != 0 {
		t.Fatalf("trashed %v", e.shell.trashed)
	}
	// the trash category is not in a junk scan unless it holds something, and
	// junk scans know nothing of paths: the only way to the Trash is a junk id.
	junkScan, _ := e.scanJunk(t)
	_, err = e.svc.Confirm.Clean(junkScan, []string{"trash", "nope"})
	wantErr(t, err, "Invalid request")
}

func TestTrashPathsRefusesWithoutAMatchingToken(t *testing.T) {
	e := newEnv(t)
	scanID, p := e.largeScan(t, "a.bin", "b.bin")
	run := func(paths []string, tok string) error {
		_, err := e.svc.Files.TrashPaths(context.Background(), rid(), scanID, paths, tok)
		return err
	}
	mint := func(paths ...string) string {
		tok, err := e.svc.Confirm.Trash(scanID, paths)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	wantErr(t, run(p[:1], ""), errRequired)
	wantErr(t, run(p[:1], "nope"), errRequired)
	wantErr(t, run(p, mint(p[0])), errRequired) // {a} does not cover {a, b}
	exp := mint(p[0])
	e.advance(3 * time.Minute)
	wantErr(t, run(p[:1], exp), errExpired)
	if len(e.shell.trashed) != 0 {
		t.Fatalf("trashed %v", e.shell.trashed)
	}
	tok := mint(p[1], p[0], p[1]) // order and repeats do not matter
	if err := run(p, tok); err != nil {
		t.Fatal(err)
	}
	if len(e.shell.trashed) != 2 {
		t.Fatalf("trashed %v", e.shell.trashed)
	}
	wantErr(t, run(p, tok), errRequired)
}

func TestApplicationsAndStartupNeedTokens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	listID := rid()
	if _, err := e.svc.Apps.List(ctx, listID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"", "nope"} {
		_, err := e.svc.Apps.Uninstall(ctx, listID, "mac:abc", tok)
		wantErr(t, err, errRequired)
		_, err = e.svc.Startup.Set(ctx, "mac:user:x", true, tok)
		wantErr(t, err, errRequired)
	}
	// an id the list does not hold gets no token
	_, err := e.svc.Confirm.Uninstall(listID, "mac:abc")
	wantErr(t, err, "Invalid request")
	_, err = e.svc.Confirm.Uninstall(rid(), "mac:abc")
	wantErr(t, err, errScanExpired)

	// startup: the token is for this id and this state only
	on, _ := e.svc.Confirm.Startup("mac:user:x", true)
	_, err = e.svc.Startup.Set(ctx, "mac:user:x", false, on)
	wantErr(t, err, errRequired)
	on, _ = e.svc.Confirm.Startup("mac:user:x", true)
	_, err = e.svc.Startup.Set(ctx, "mac:user:y", true, on)
	wantErr(t, err, errRequired)
	// the id and the state cannot be swapped for each other
	a, _ := e.svc.Confirm.Startup("true", false)
	_, err = e.svc.Startup.Set(ctx, "false", true, a)
	wantErr(t, err, errRequired)
	// a matching token passes the gate (off a Mac the toggle itself then fails)
	ok, _ := e.svc.Confirm.Startup("mac:user:x", true)
	if _, err = e.svc.Startup.Set(ctx, "mac:user:x", true, ok); err != nil && strings.HasPrefix(err.Error(), "Confirmation") {
		t.Fatalf("matching token refused: %v", err)
	}
}

// H1: results older than ten minutes are not acted on.
func TestScanTTL(t *testing.T) {
	e := newEnv(t)
	scanID, p := e.largeScan(t, "a.bin")
	e.advance(9 * time.Minute)
	if _, err := e.svc.Confirm.Trash(scanID, p); err != nil {
		t.Fatalf("9 minutes: %v", err)
	}
	e.advance(2 * time.Minute) // 11 minutes in total
	_, err := e.svc.Confirm.Trash(scanID, p)
	wantErr(t, err, errScanExpired)
}

// H1: a file that changed after the scan is skipped and reported.
func TestTrashSkipsFilesChangedSinceTheScan(t *testing.T) {
	e := newEnv(t)
	scanID, p := e.largeScan(t, "a.bin", "b.bin", "c.bin")
	// b is edited after the scan: same size, newer mtime
	later := time.Now().Add(time.Hour)
	os.Chtimes(p[1], later, later)
	res, err := e.trash(scanID, p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Failed != 1 {
		t.Fatalf("res = %+v", res)
	}
	if !slices.Contains(res.Skipped, "1 file changed since the scan") {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	if slices.Contains(e.shell.trashed, p[1]) || !fileExists(p[1]) {
		t.Fatalf("the changed file was trashed: %v", e.shell.trashed)
	}
}

func TestTrashSkipsFilesGrownSinceTheScan(t *testing.T) {
	e := newEnv(t)
	scanID, p := e.largeScan(t, "a.bin")
	st, _ := os.Lstat(p[0])
	f, _ := os.OpenFile(p[0], os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("more")
	f.Close()
	os.Chtimes(p[0], st.ModTime(), st.ModTime()) // even with the old mtime
	res, err := e.trash(scanID, p)
	if err != nil || res.Removed != 0 || res.Failed != 1 || len(e.shell.trashed) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

// H1: a duplicate is moved only while a copy that stays still exists and still
// holds the same content.
func TestTrashDuplicateNeedsTheKeptCopyIntact(t *testing.T) {
	setup := func(t *testing.T) (*env, string, string, string, string) {
		e := newEnv(t)
		keep := filepath.Join(e.home, "Movies", "keep.bin")
		dup := filepath.Join(e.home, "Movies", "dup.bin")
		put(t, keep, 1<<20, 48*time.Hour)
		put(t, dup, 1<<20, 49*time.Hour)
		id := rid()
		scan, err := e.svc.Files.ScanDuplicates(context.Background(), id)
		groups := scan.Groups
		if err != nil || len(groups) != 1 || len(groups[0].Files) != 2 {
			t.Fatalf("groups = %+v err = %v", groups, err)
		}
		if groups[0].Files[0].Path != keep { // the newer copy is the first
			t.Fatalf("group order = %+v", groups[0].Files)
		}
		return e, id, keep, dup, groups[0].Hash
	}

	t.Run("intact", func(t *testing.T) {
		e, id, _, dup, _ := setup(t)
		res, err := e.trash(id, []string{dup})
		if err != nil || res.Removed != 1 || res.Failed != 0 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("kept copy edited in place keeping size and mtime", func(t *testing.T) {
		e, id, keep, dup, _ := setup(t)
		st, _ := os.Lstat(keep)
		buf := make([]byte, st.Size())
		buf[0] = 1
		os.WriteFile(keep, buf, 0o644)
		os.Chtimes(keep, st.ModTime(), st.ModTime())
		res, err := e.trash(id, []string{dup})
		if err != nil || res.Removed != 0 || res.Failed != 1 || len(e.shell.trashed) != 0 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if !fileExists(dup) {
			t.Fatal("the only intact copy was trashed")
		}
	})
	t.Run("kept copy deleted", func(t *testing.T) {
		e, id, keep, dup, _ := setup(t)
		os.Remove(keep)
		res, err := e.trash(id, []string{dup})
		if err != nil || res.Removed != 0 || res.Failed != 1 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("every copy selected", func(t *testing.T) {
		e, id, keep, dup, _ := setup(t)
		res, err := e.trash(id, []string{keep, dup})
		if err != nil || res.Removed != 0 || res.Failed != 2 || len(e.shell.trashed) != 0 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
}

// M4: a move to the Trash can be stopped from the page.
func TestTrashPathsCanBeCancelled(t *testing.T) {
	e := newEnv(t)
	scanID, p := e.largeScan(t, "a.bin", "b.bin", "c.bin")
	tok, err := e.svc.Confirm.Trash(scanID, p)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.shell.onTrash = func(string) {
		once.Do(func() { close(started) })
		<-release
	}
	runID := rid()
	type out struct {
		res model.CleanResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := e.svc.Files.TrashPaths(context.Background(), runID, scanID, p, tok)
		done <- out{r, err}
	}()
	<-started
	if !e.svc.System.CancelScan(runID) {
		t.Fatal("the trash run is not cancellable")
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !got.res.Cancelled || got.res.Removed != 1 || got.res.Failed != 2 {
		t.Fatalf("res = %+v", got.res)
	}
	if e.svc.System.CancelScan(runID) {
		t.Fatal("finished run still registered")
	}
}

// M6: two scans of one task run side by side, each with its own progress and cancel.
func TestConcurrentScansOfOneTaskAreIndependent(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Documents", "a.txt"), 10, 0)
	idA, idB := rid(), rid()
	started := make(chan string, 2)
	release := make(chan struct{})
	var seen sync.Map
	e.svc.System.c.deps.Sink = func(p model.ScanProgress) {
		seen.Store(p.ID, true)
		if _, first := seen.LoadOrStore("parked-"+p.ID, true); !first {
			started <- p.ID
			<-release
		}
	}
	type out struct {
		node model.SpaceNode
		err  error
	}
	res := map[string]chan out{idA: make(chan out, 1), idB: make(chan out, 1)}
	for id, ch := range res {
		go func() {
			n, err := e.svc.Files.ScanSpaces(context.Background(), id, "")
			ch <- out{n, err}
		}()
	}
	<-started
	<-started
	if !e.svc.System.CancelScan(idA) {
		t.Fatal("scan A not found")
	}
	close(release)
	a, b := <-res[idA], <-res[idB]
	if !errors.Is(a.err, progress.ErrCancelled) {
		t.Fatalf("A: err = %v", a.err)
	}
	if b.err != nil || b.node.Name != "Home" {
		t.Fatalf("B was disturbed by the cancel of A: %+v %v", b.node, b.err)
	}
	for _, id := range []string{idA, idB} {
		if _, ok := seen.Load(id); !ok {
			t.Errorf("no progress carried id %s", id)
		}
	}
}

func TestRunIDsAreValidated(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"", "short", "has spaces in it 1234", "../../etc/passwd-padding"} {
		if _, err := e.svc.Junk.Scan(context.Background(), bad); err == nil {
			t.Errorf("run id %q accepted", bad)
		}
	}
	id := rid()
	if _, err := e.svc.Junk.Scan(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// the id of a remembered scan cannot be used again
	if _, err := e.svc.Junk.Scan(context.Background(), id); err == nil {
		t.Error("a stored scan's id was reused")
	}
	if _, err := e.svc.Files.TrashPaths(context.Background(), id, id, []string{"/x"}, "t"); err == nil {
		t.Error("a stored scan's id was reused as a run id")
	}
}

// The 24 hour cutoff a clean applies is the one the scan showed: a file that
// was 23h59m old at scan time stays even if the clean runs minutes later.
func TestCleanUsesTheScanTimeCutoff(t *testing.T) {
	e := newEnv(t)
	young := filepath.Join(e.home, "Library", "Logs", "young", "y.log")
	old := filepath.Join(e.home, "Library", "Logs", "old", "o.log")
	put(t, young, 100, 23*time.Hour+59*time.Minute)
	put(t, old, 100, 48*time.Hour) // keeps the category listed
	scanID, cats := e.scanJunk(t)
	if len(cats) != 1 || cats[0].Size != 100 {
		t.Fatalf("the scan showed %+v", cats)
	}
	e.advance(2 * time.Minute)
	res, err := e.clean(scanID, []string{"mac-logs"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Freed != 100 || fileExists(old) {
		t.Fatalf("res = %+v", res)
	}
	if !fileExists(young) {
		t.Fatal("a file the scan did not offer was deleted")
	}
}

// Every CleanResult the page can receive has arrays, never null.
func TestCleanResultJSONHasNoNullSlices(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Library", "Logs", "a", "x.log"), 10, 48*time.Hour)
	scanID, _ := e.scanJunk(t)
	clean, err := e.clean(scanID, []string{"mac-logs"})
	if err != nil {
		t.Fatal(err)
	}
	lscan, p := e.largeScan(t, "a.bin", "b.bin")
	trashed, err := e.trash(lscan, p[:1])
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := e.svc.Confirm.Trash(lscan, p[1:])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled, err := e.svc.Files.TrashPaths(ctx, rid(), lscan, p[1:], tok)
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]model.CleanResult{"clean": clean, "trash": trashed, "cancelled": cancelled, "noResult": noResult()} {
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), "null") {
			t.Errorf("%s: %s", name, b)
		}
	}
	if !cancelled.Cancelled {
		t.Errorf("cancelled = %+v", cancelled)
	}
}

// S2: a file trashed after the junk scan is not erased by a clean confirmed
// against that scan.
func TestTrashAfterJunkScanSurvivesTheClean(t *testing.T) {
	e := newEnv(t)
	trashDir := filepath.Join(e.home, ".Trash")
	scannedFile := filepath.Join(trashDir, "old.bin")
	put(t, scannedFile, 4096, 48*time.Hour)
	junkScan, cats := e.scanJunk(t)
	if len(cats) != 1 || cats[0].ID != "trash" {
		t.Fatalf("cats = %+v", cats)
	}
	// Large Files moves a file into the Trash after the scan, as the Finder would
	e.shell.onTrash = func(p string) { os.Rename(p, filepath.Join(trashDir, filepath.Base(p))) }
	scanID, paths := e.largeScan(t, "precious.bin")
	if _, err := e.trash(scanID, paths); err != nil {
		t.Fatal(err)
	}
	e.advance(30 * time.Second)
	res, err := e.clean(junkScan, []string{"trash"})
	if err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(trashDir, "precious.bin")) {
		t.Fatal("a file trashed after the junk scan was permanently deleted")
	}
	if fileExists(scannedFile) || res.Removed != 1 {
		t.Fatalf("the scanned entry should be gone: res = %+v", res)
	}
}

func dupSetup(t *testing.T) (*env, string, string, string) {
	t.Helper()
	e := newEnv(t)
	keep := filepath.Join(e.home, "Movies", "keep.bin")
	dup := filepath.Join(e.home, "Movies", "dup.bin")
	put(t, keep, 1<<20, 48*time.Hour)
	put(t, dup, 1<<20, 49*time.Hour)
	e.shell.onTrash = func(p string) { os.Rename(p, p+".trashed") }
	id := rid()
	if _, err := e.svc.Files.ScanDuplicates(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return e, id, keep, dup
}

// H1: two overlapping calls, each trashing the other's kept copy, must not
// remove every copy of a group.
func TestConcurrentTrashCannotRemoveEveryCopy(t *testing.T) {
	e, id, keep, dup := dupSetup(t)
	var mu sync.Mutex
	arrived := 0
	both := make(chan struct{})
	e.svc.Files.c.hash = func(ctx context.Context, p string) (string, error) {
		h, err := dupes.FullHash(ctx, p)
		mu.Lock()
		arrived++
		if arrived == 2 {
			close(both)
		}
		mu.Unlock()
		// without the lock both calls are here together; with it the first waits in vain
		select {
		case <-both:
		case <-time.After(400 * time.Millisecond):
		}
		return h, err
	}
	tk1, _ := e.svc.Confirm.Trash(id, []string{dup})
	tk2, _ := e.svc.Confirm.Trash(id, []string{keep})
	var wg sync.WaitGroup
	for _, c := range []struct {
		path, tok string
	}{{dup, tk1}, {keep, tk2}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.svc.Files.TrashPaths(context.Background(), rid(), id, []string{c.path}, c.tok)
		}()
	}
	wg.Wait()
	if !fileExists(keep) && !fileExists(dup) {
		t.Fatal("every copy of the group was trashed by two overlapping calls")
	}
}

func TestWaitingForTheMoveLockCanBeCancelled(t *testing.T) {
	e, id, keep, dup := dupSetup(t)
	_ = keep
	tok, _ := e.svc.Confirm.Trash(id, []string{dup})
	e.svc.Files.c.moves <- struct{}{} // another call is moving files
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Files.TrashPaths(ctx, rid(), id, []string{dup}, tok)
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, progress.ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
	if !fileExists(dup) {
		t.Fatal("moved while cancelled")
	}
}

// H1: the file is swapped for another one (same size and mtime) after the
// recheck and before the move: it must not be moved.
func TestTrashRefusesAFileSwappedAfterTheRecheck(t *testing.T) {
	e, id, keep, dup := dupSetup(t)
	_ = keep
	real := e.svc.Files.c.hash
	e.svc.Files.c.hash = func(ctx context.Context, p string) (string, error) {
		h, err := real(ctx, p)
		// the recheck has already accepted dup's stamp; now replace it
		st, _ := os.Lstat(dup)
		other := dup + ".new"
		put(t, other, int(st.Size()), 49*time.Hour)
		os.Chtimes(other, st.ModTime(), st.ModTime())
		os.Rename(other, dup)
		return h, err
	}
	res, err := e.trash(id, []string{dup})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || res.Failed != 1 || len(e.shell.trashed) != 0 {
		t.Fatalf("res = %+v trashed = %v", res, e.shell.trashed)
	}
	if !fileExists(dup) {
		t.Fatal("the swapped file was trashed")
	}
}

// H1: the kept copy is hashed without following a link.
func TestKeptCopyReplacedByALinkIsNotAcceptedByTheHash(t *testing.T) {
	e, id, keep, dup := dupSetup(t)
	// the real hash function of the service refuses links outright
	h := e.svc.Files.c.hash
	link := keep + ".lnk"
	os.Symlink(dup, link)
	if _, err := h(context.Background(), link); err == nil {
		t.Fatal("a link was hashed")
	}
	_ = id
}

// M6: an id is not reusable while a token for it may still be valid, even
// when the snapshot behind it was evicted.
func TestRunIDStaysTakenWhileATokenIsLive(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Library", "Logs", "a", "x.log"), 10, 48*time.Hour)
	scanID, _ := e.scanJunk(t)
	if _, err := e.svc.Confirm.Clean(scanID, []string{"mac-logs"}); err != nil {
		t.Fatal(err)
	}
	// twelve newer scans push the first snapshot out
	for range 12 {
		e.svc.Files.c.snaps.Put(snapshot.Apps(rid(), nil))
	}
	if e.svc.Files.c.snaps.Has(scanID) {
		t.Fatal("the snapshot should have been evicted")
	}
	if _, err := e.svc.Junk.Scan(context.Background(), scanID); err == nil {
		t.Fatal("the id of a scan with a live token was reused")
	}
	e.advance(3 * time.Minute)
	if _, err := e.svc.Junk.Scan(context.Background(), scanID); err != nil {
		t.Fatalf("free again once the token expired: %v", err)
	}
}

// A refused id is not left registered (it could never be cancelled or reused).
func TestRefusedRunIDIsReleased(t *testing.T) {
	e := newEnv(t)
	id := rid()
	if _, err := e.svc.Junk.Scan(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.svc.Junk.c.begin(context.Background(), id, model.TaskLarge); err == nil {
		t.Fatal("stored id accepted")
	}
	if e.svc.Junk.c.scans.Running(id) {
		t.Fatal("the refused id stayed registered")
	}
}

// A scan whose id got stored behind its back does not overwrite it.
func TestScanDoesNotReplaceAStoredSnapshot(t *testing.T) {
	e := newEnv(t)
	id := rid()
	e.svc.Junk.c.snaps.Put(snapshot.Apps(id, []model.InstalledApp{{ID: "mac:1"}}))
	// bypass begin's check, as a racing scan would
	if err := e.svc.Junk.c.snaps.Put(snapshot.Large(id, nil)); err == nil {
		t.Fatal("Put replaced a stored snapshot")
	}
	if _, err := e.svc.Confirm.Uninstall(id, "mac:1"); err != nil {
		t.Fatalf("original snapshot lost: %v", err)
	}
}

// Files the Trash guard would refuse (a credentials or model store, a folder
// shared by Syncthing) are not offered in the first place.
func TestLargeFilesListsOnlyTrashableFiles(t *testing.T) {
	e := newEnv(t)
	ok := filepath.Join(e.home, "Downloads", "ok.bin")
	put(t, ok, 1<<20+1, 48*time.Hour)
	put(t, filepath.Join(e.home, ".ollama", "models", "blobs", "m.bin"), 1<<20+1, 48*time.Hour)
	put(t, filepath.Join(e.home, ".claude", "projects", "big.bin"), 1<<20+1, 48*time.Hour)
	put(t, filepath.Join(e.home, "Work", "shared", ".stfolder"), 1, 48*time.Hour)
	put(t, filepath.Join(e.home, "Work", "shared", "s.bin"), 1<<20+1, 48*time.Hour)
	scan, err := e.svc.Files.ScanLargeFiles(context.Background(), rid(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files := scan.Files
	if len(files) != 1 || files[0].Path != ok {
		t.Fatalf("files = %+v, want only %s", files, ok)
	}
	// the three refused files are counted, so that the page can say so
	if scan.Hidden != 3 {
		t.Fatalf("hidden = %d, want 3", scan.Hidden)
	}
}

func TestDuplicatesListOnlyTrashableCopies(t *testing.T) {
	e := newEnv(t)
	a := filepath.Join(e.home, "Movies", "a.bin")
	b := filepath.Join(e.home, "Movies", "b.bin")
	shared := filepath.Join(e.home, "Documents", "shared", "c.bin")
	put(t, filepath.Join(e.home, "Documents", "shared", ".stfolder"), 1, 48*time.Hour)
	put(t, a, 1<<20, 48*time.Hour)
	put(t, b, 1<<20, 49*time.Hour)
	put(t, shared, 1<<20, 50*time.Hour)
	// a pair whose second copy cannot be trashed is no pair
	put(t, filepath.Join(e.home, "Movies", "p.bin"), 1<<20+7, 48*time.Hour)
	put(t, filepath.Join(e.home, "Documents", "shared", "p.bin"), 1<<20+7, 48*time.Hour)
	scan, err := e.svc.Files.ScanDuplicates(context.Background(), rid())
	if err != nil {
		t.Fatal(err)
	}
	groups := scan.Groups
	// the shared copy of the first group, and both copies of the pair (one
	// refused, one left alone), are not shown
	if scan.Hidden != 3 {
		t.Fatalf("hidden = %d, want 3", scan.Hidden)
	}
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("groups = %+v, want one group of the two trashable copies", groups)
	}
	for _, f := range groups[0].Files {
		if f.Path == shared {
			t.Fatalf("refused copy listed: %+v", groups[0].Files)
		}
	}
}

func dupeGroups(groups, copies int) []model.DuplicateGroup {
	var out []model.DuplicateGroup
	for g := range groups {
		dg := model.DuplicateGroup{Hash: strconv.Itoa(g), Size: 1}
		for c := range copies {
			dg.Files = append(dg.Files, model.DuplicateFile{Path: "/g" + strconv.Itoa(g) + "/c" + strconv.Itoa(c)})
		}
		out = append(out, dg)
	}
	return out
}

// The copies are judged in parallel (never more than dupeCheckers at once),
// the groups and copies keep their order, and progress is reported.
func TestOnlyTrashableKeepsOrderAndBoundsParallelism(t *testing.T) {
	groups := dupeGroups(30, 3)
	var inFlight, peak atomic.Int32
	allow := func(p string) bool {
		n := inFlight.Add(1)
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
		// refuse the third copy of every even group, and the second of group 5
		return !(strings.HasSuffix(p, "/c2") && strings.HasPrefix(p, "/g") && groupNo(p)%2 == 0) && p != "/g5/c1"
	}
	var reports atomic.Int32
	rep := progress.Reporter(func(string, int, int, string) { reports.Add(1) })
	got, hidden, err := onlyTrashable(context.Background(), groups, allow, rep)
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() > dupeCheckers || peak.Load() < 2 {
		t.Fatalf("peak parallelism = %d, want between 2 and %d", peak.Load(), dupeCheckers)
	}
	if reports.Load() == 0 {
		t.Error("no progress reported")
	}
	// 15 even groups lose one copy each; group 5 keeps two copies of three
	if len(got) != 30 || hidden != 15+1 {
		t.Fatalf("groups = %d hidden = %d, want 30 and 16", len(got), hidden)
	}
	for i, g := range got {
		if g.Hash != strconv.Itoa(i) {
			t.Fatalf("group %d out of order: %s", i, g.Hash)
		}
		want := 3
		if i%2 == 0 || i == 5 {
			want = 2
		}
		if len(g.Files) != want {
			t.Fatalf("group %d has %d copies, want %d", i, len(g.Files), want)
		}
	}
	if len(groups[0].Files) != 3 {
		t.Error("input groups were changed")
	}
}

func groupNo(p string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.SplitN(p, "/", 3)[1], "g"))
	return n
}

// A cancelled run stops judging copies and gives progress.ErrCancelled.
func TestOnlyTrashableStopsWhenCancelled(t *testing.T) {
	groups := dupeGroups(200, 5) // 1000 copies
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	allow := func(string) bool {
		if calls.Add(1) == 20 {
			cancel()
		}
		time.Sleep(time.Millisecond)
		return true
	}
	got, hidden, err := onlyTrashable(ctx, groups, allow, nil)
	if !errors.Is(err, progress.ErrCancelled) || got != nil || hidden != 0 {
		t.Fatalf("got %v, %d, %v; want nil, 0, ErrCancelled", got, hidden, err)
	}
	if n := calls.Load(); n >= 200 {
		t.Fatalf("%d copies judged after cancelling at 20", n)
	}
}

func BenchmarkOnlyTrashable(b *testing.B) {
	groups := dupeGroups(100, 3)
	allow := func(string) bool { time.Sleep(50 * time.Microsecond); return true }
	for range b.N {
		if _, _, err := onlyTrashable(context.Background(), groups, allow, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// A folder last changed long ago whose access time is older still (the scan's
// own reading of it refreshes that) is cleaned after the scan listed it, and a
// folder swapped for another after the scan is not.
func TestOldDownloadsFolderListedByTheScanIsCleaned(t *testing.T) {
	mk := func(t *testing.T, e *env, name string) string {
		t.Helper()
		folder := filepath.Join(e.home, "Downloads", name)
		file := filepath.Join(folder, "a.bin")
		put(t, file, 100, 200*24*time.Hour)
		now := time.Now()
		os.Chtimes(file, now.Add(-200*24*time.Hour), now.Add(-200*24*time.Hour))
		os.Chtimes(folder, now.Add(-300*24*time.Hour), now.Add(-200*24*time.Hour)) // atime before mtime
		return folder
	}
	t.Run("cleaned", func(t *testing.T) {
		e := newEnv(t)
		e.clock = time.Now().Add(120 * 24 * time.Hour) // the status change time (now) is then old too
		folder := mk(t, e, "old")
		e.shell.onTrash = func(p string) { os.Rename(p, p+".trashed") }
		scanID, cats := e.scanJunk(t)
		if len(cats) != 1 || cats[0].ID != "old-downloads" {
			t.Fatalf("cats = %+v", cats)
		}
		res, err := e.clean(scanID, []string{"old-downloads"})
		if err != nil || res.Removed != 1 || fileExists(folder) {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("swapped after the scan", func(t *testing.T) {
		e := newEnv(t)
		e.clock = time.Now().Add(120 * 24 * time.Hour) // the status change time (now) is then old too
		folder := mk(t, e, "old")
		e.shell.onTrash = func(p string) { os.Rename(p, p+".trashed") }
		scanID, _ := e.scanJunk(t)
		os.Rename(folder, folder+"-moved")
		mk(t, e, "old")
		res, err := e.clean(scanID, []string{"old-downloads"})
		if err != nil || res.Removed != 0 || res.Failed != 1 || !fileExists(folder) {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
}
