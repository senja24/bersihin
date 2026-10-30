package junk

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

// S3: the category says whether a clean deletes for good.
func TestIrreversibleFlag(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil || len(cats) == 0 {
		t.Fatalf("cats = %v err = %v", cats, err)
	}
	seen := map[string]bool{}
	for _, c := range cats {
		seen[c.ID] = true
		want := c.ID != "old-downloads"
		if c.Irreversible != want {
			t.Errorf("%s: Irreversible = %v, want %v", c.ID, c.Irreversible, want)
		}
	}
	for _, id := range []string{"mac-caches", "trash", "old-downloads"} {
		if !seen[id] {
			t.Errorf("%s not scanned", id)
		}
	}
}

// S8: a folder macOS closes to us is counted, not silently skipped.
func TestDeniedSubfolderIsCounted(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "ok", "a"), 10, old)
	closed := filepath.Join(root, "app", "closed")
	put(t, filepath.Join(closed, "b"), 10, old)
	if err := os.Chmod(closed, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(closed, 0o755)

	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Denied != 1 || out.Failed != 0 || out.Removed != 1 {
		t.Fatalf("out = %+v", out)
	}
	if exists(filepath.Join(root, "app", "ok", "a")) {
		t.Error("the readable part should still be cleaned")
	}
}

func TestUnreadableRootIsDenied(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Logs")
	put(t, filepath.Join(root, "app", "x.log"), 10, old)
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0o755)

	res, err := f.sc.Clean(context.Background(), []string{"mac-logs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied != 1 || res.Failed != 0 || res.Removed != 0 {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("denied folders are counted in Denied, not in Skipped: %v", res.Skipped)
	}
}

func TestDeniedSubfolderThroughClean(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Logs")
	put(t, filepath.Join(root, "app", "x.log"), 10, old)
	closed := filepath.Join(root, "app", "closed")
	put(t, filepath.Join(closed, "y"), 1, old)
	os.Chmod(closed, 0)
	defer os.Chmod(closed, 0o755)

	res, _ := f.sc.Clean(context.Background(), []string{"mac-logs"}, nil)
	if res.Denied != 1 || res.Failed != 0 || res.Freed != 10 {
		t.Fatalf("res = %+v", res)
	}
}

// Any other error on a root counts as failed, not as cleaned and not as denied.
func TestOtherRootErrorIsFailed(t *testing.T) {
	f := newFixture(t)
	notAFolder := put(t, f.p("Library", "Logs"), 1, old) // a file where the root should be
	def := Def{ID: "x", Name: "x", Roots: func() []Root { return []Root{{Path: notAFolder}} }}
	got := f.sc.cleanCache(context.Background(), def)
	if got.failed != 1 || got.denied != 0 || got.removed != 0 {
		t.Fatalf("tally = %+v", got)
	}
}

func TestDescendQuietlySkipsWhatIsNotAFolder(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	os.MkdirAll(root, 0o755)
	put(t, filepath.Join(root, "afile"), 1, old)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, _, st := descend(r, "afile"); st != descSkip {
		t.Fatalf("a file is skipped quietly, status = %v", st)
	}
	if _, _, st := descend(r, "missing"); st != descSkip {
		t.Fatalf("a vanished folder is skipped quietly, status = %v", st)
	}
}

func TestOldDownloadsTrashLineIsANote(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	res, err := f.sc.Clean(context.Background(), []string{"old-downloads"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Notes, "Old downloads went to the Trash, so you can restore them") {
		t.Fatalf("notes = %v", res.Notes)
	}
	if len(res.Skipped) != 0 || res.Failed != 0 || res.Denied != 0 {
		t.Fatalf("a successful Downloads clean has no problems: %+v", res)
	}
	if res.Skipped == nil || res.Notes == nil {
		t.Fatal("slices must not be nil")
	}
}

func TestCleanCancelledSetsFlagNotText(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := f.sc.Clean(ctx, []string{"mac-caches"}, nil)
	if err != nil || !res.Cancelled {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestFinishedCleanIsNotCancelled(t *testing.T) {
	f := newFixture(t)
	put(t, f.p("Library", "Logs", "a.log"), 5, 48*time.Hour)
	res, _ := f.sc.Clean(context.Background(), []string{"mac-logs"}, nil)
	if res.Cancelled || res.Skipped == nil || res.Notes == nil {
		t.Fatalf("res = %+v", res)
	}
}

// S8: a folder we can list but not search is counted (on Linux the open of the
// folder itself is already denied; the per-entry Lstat is the macOS variant).
func TestUnsearchableFolderIsCounted(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Caches")
	ro := filepath.Join(root, "app", "ro")
	put(t, filepath.Join(ro, "a"), 1, old)
	put(t, filepath.Join(ro, "b"), 1, old)
	os.Chmod(ro, 0o444) // readable, not searchable
	defer os.Chmod(ro, 0o755)

	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Denied < 1 || out.Failed != 0 {
		t.Fatalf("out = %+v", out)
	}
}

// S8: an emptied folder that cannot be removed is counted.
func TestFolderRemoveErrorIsCounted(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Caches")
	parent := filepath.Join(root, "app", "locked-parent")
	os.MkdirAll(filepath.Join(parent, "empty"), 0o755)
	os.Chmod(parent, 0o555)
	defer os.Chmod(parent, 0o755)

	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Denied != 1 {
		t.Fatalf("nested folder: out = %+v", out)
	}
	if !exists(filepath.Join(parent, "empty")) {
		t.Fatal("the folder should still be there")
	}
}

func TestTopFolderRemoveErrorIsCounted(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	root := f.p("Library", "Caches")
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	os.Chmod(root, 0o555)
	defer os.Chmod(root, 0o755)

	out := DeleteEntry(context.Background(), filepath.Join(root, "empty"), root, DeleteOptions{Home: f.home})
	if out.Denied != 1 {
		t.Fatalf("out = %+v", out)
	}
}

// A folder that stays because something fresh is inside is not an error.
func TestNonEmptyFolderIsQuiet(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "sub", "fresh"), 1, time.Minute)
	put(t, filepath.Join(root, "app", "sub", "old"), 1, old)
	opt := DeleteOptions{Home: f.home, ModifiedBefore: time.Now().Add(-24 * time.Hour)}
	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, opt)
	if out.Denied != 0 || out.Failed != 0 || out.Refused || out.Removed != 1 {
		t.Fatalf("out = %+v", out)
	}
	// and a folder that vanished meanwhile is quiet as well
	var o Outcome
	if o.removed(os.ErrNotExist); o.Denied+o.Failed != 0 {
		t.Fatalf("o = %+v", o)
	}
}

// A vanished root is nothing to clean, not "linked items skipped for safety".
func TestVanishedRootIsNotARefusal(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "x"), 1, old)
	target := filepath.Join(root, "app")
	os.RemoveAll(root)
	out := DeleteEntry(context.Background(), target, root, DeleteOptions{Home: f.home})
	if out.Refused || out.Denied+out.Failed != 0 || out.Removed != 0 {
		t.Fatalf("out = %+v", out)
	}
}

// S8: an unreadable ~/Downloads at clean time is reported, not "nothing to clean".
func TestUnreadableDownloadsIsDenied(t *testing.T) {
	skipIfRoot(t)
	f := newFixture(t)
	dl := f.p("Downloads")
	put(t, filepath.Join(dl, "ancient.dmg"), 10, old)
	os.Chmod(dl, 0)
	defer os.Chmod(dl, 0o755)

	res, err := f.sc.Clean(context.Background(), []string{"old-downloads"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied != 1 || res.Removed != 0 || res.Failed != 0 {
		t.Fatalf("res = %+v", res)
	}
	if len(f.trashed) != 0 {
		t.Fatalf("trashed %v", f.trashed)
	}
}

// S2: a clean confirmed against a scan empties the Trash entries that scan
// saw, not whatever is in the Trash by then.
func TestCleanTrashOnlyRemovesWhatTheScanSaw(t *testing.T) {
	f := newFixture(t)
	scanned := put(t, f.p(".Trash", "scanned.bin"), 100, old)
	scannedDir := put(t, f.p(".Trash", "dir", "inner"), 100, old)
	replaced := put(t, f.p(".Trash", "replaced.bin"), 100, old)
	_, entries, err := f.sc.ScanState(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %v", entries)
	}
	// after the scan: a new file arrives, and one name is deleted and recreated
	later := put(t, f.p(".Trash", "arrived-later.bin"), 100, 0)
	// the new file exists while the old one does, so the inode cannot be reused
	repl := put(t, f.p("replacement.tmp"), 100, old)
	if err := os.Rename(repl, replaced); err != nil {
		t.Fatal(err)
	}

	f.sc.TrashEntries = entries
	res, err := f.sc.Clean(context.Background(), []string{"trash"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exists(scanned) || exists(scannedDir) {
		t.Error("what the scan saw should be emptied")
	}
	if !exists(later) {
		t.Error("a file that arrived after the scan was deleted")
	}
	if !exists(replaced) {
		t.Error("an entry replaced after the scan was deleted")
	}
	if res.Removed != 2 {
		t.Fatalf("res = %+v", res)
	}
}

func TestCleanTrashWithoutStateEmptiesEverything(t *testing.T) {
	f := newFixture(t)
	a := put(t, f.p(".Trash", "a"), 10, old)
	f.sc.Clean(context.Background(), []string{"trash"}, nil)
	if exists(a) {
		t.Error("a Scanner without scan state keeps its old behaviour")
	}
}

func TestScanStateDoesNotChangeScan(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	a, _ := f.sc.Scan(context.Background(), nil)
	b, _, _ := f.sc.ScanState(context.Background(), nil)
	if len(a) != len(b) {
		t.Fatalf("%d vs %d categories", len(a), len(b))
	}
}
