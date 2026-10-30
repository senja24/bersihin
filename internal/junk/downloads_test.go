package junk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/sys"
)

// at sets the access and modification time of p, given as ages.
func at(t *testing.T, p string, atimeAge, mtimeAge time.Duration) {
	t.Helper()
	now := time.Now()
	if err := os.Chtimes(p, now.Add(-atimeAge), now.Add(-mtimeAge)); err != nil {
		t.Fatal(err)
	}
}

func hasCategory(cats []model.JunkCategory, id string) bool {
	return slices.ContainsFunc(cats, func(c model.JunkCategory) bool { return c.ID == id })
}

func clean(t *testing.T, sc *Scanner) model.CleanResult {
	t.Helper()
	res, err := sc.Clean(context.Background(), []string{"old-downloads"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Reading a folder updates its access time when that is older than its
// modification time (APFS, Linux relatime), so the scan itself used to make a
// folder look used and the clean then skipped what the scan had just listed.
// The access time of a folder is not looked at at all.
func TestFolderWithOldAtimeAndNewerMtimeIsCleanedAfterAScan(t *testing.T) {
	f := newFixture(t)
	folder := f.p("Downloads", "oldfolder")
	file := put(t, filepath.Join(folder, "a.bin"), 100, old)
	at(t, file, old, old)
	at(t, folder, old+100*24*time.Hour, old) // access time before modification time

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil || !hasCategory(cats, "old-downloads") {
		t.Fatalf("scan = %+v, %v; want old-downloads", cats, err)
	}
	res := clean(t, f.sc)
	if !slices.Equal(f.trashed, []string{folder}) {
		t.Fatalf("trashed = %v, want %s (res %+v)", f.trashed, folder, res)
	}
}

// A folder's own access time, however recent, does not count as use.
func TestFolderAccessTimeIsNotUse(t *testing.T) {
	f := newFixture(t)
	folder := f.p("Downloads", "listed")
	file := put(t, filepath.Join(folder, "a.bin"), 100, old)
	at(t, file, old, old)
	at(t, folder, time.Hour, old) // just read, long unchanged

	r := f.sc.findOldDownloads(context.Background())
	if !slices.Equal(r.paths, []string{folder}) {
		t.Fatalf("paths = %v, want %s", r.paths, folder)
	}
}

// A file inside a folder that was opened within 90 days keeps the folder.
func TestFolderWithRecentlyOpenedFileIsKept(t *testing.T) {
	f := newFixture(t)
	folder := f.p("Downloads", "project")
	opened := put(t, filepath.Join(folder, "doc.pdf"), 100, old)
	put(t, filepath.Join(folder, "sub", "b.bin"), 100, old)
	at(t, opened, 10*24*time.Hour, old) // opened 10 days ago, last changed long ago
	at(t, f.p("Downloads", "project", "sub", "b.bin"), old, old)
	ageDirs(t, f.p("Downloads"))

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil || hasCategory(cats, "old-downloads") {
		t.Fatalf("scan = %+v, %v; want no old-downloads", cats, err)
	}
	clean(t, f.sc)
	if len(f.trashed) != 0 || !exists(folder) {
		t.Fatalf("trashed = %v", f.trashed)
	}
}

func scanned(t *testing.T, f *fixture) {
	t.Helper()
	cats, st, err := f.sc.ScanWithState(context.Background(), nil)
	if err != nil || !hasCategory(cats, "old-downloads") || len(st.Downloads) == 0 {
		t.Fatalf("scan = %+v, %+v, %v", cats, st, err)
	}
	f.sc.Downloads = st.Downloads
}

func oldFolder(t *testing.T, f *fixture, name string) string {
	t.Helper()
	folder := f.p("Downloads", name)
	file := put(t, filepath.Join(folder, "a.bin"), 100, old)
	at(t, file, old, old)
	at(t, folder, old, old)
	return folder
}

// The clean holds to what the scan saw: the same entries, not used since.
func TestCleanHoldsToTheScanVerdict(t *testing.T) {
	t.Run("unchanged entries go", func(t *testing.T) {
		f := newFixture(t)
		folder := oldFolder(t, f, "dir")
		file := put(t, f.p("Downloads", "file.dmg"), 50, old)
		scanned(t, f)
		if res := clean(t, f.sc); res.Removed != 2 || res.Failed != 0 {
			t.Fatalf("res = %+v", res)
		}
		slices.Sort(f.trashed)
		if !slices.Equal(f.trashed, []string{folder, file}) {
			t.Fatalf("trashed = %v", f.trashed)
		}
	})
	t.Run("folder replaced by another after the scan", func(t *testing.T) {
		f := newFixture(t)
		folder := oldFolder(t, f, "dir")
		scanned(t, f)
		if err := os.Rename(folder, f.p("Downloads", "moved-away")); err != nil {
			t.Fatal(err)
		}
		oldFolder(t, f, "dir") // a different folder, old as well
		res := clean(t, f.sc)
		if len(f.trashed) != 0 || res.Failed != 1 {
			t.Fatalf("trashed = %v res = %+v, want the new folder kept", f.trashed, res)
		}
	})
	t.Run("file replaced by another after the scan", func(t *testing.T) {
		f := newFixture(t)
		file := put(t, f.p("Downloads", "a.dmg"), 50, old)
		scanned(t, f)
		// a second link keeps the old inode in use, so that the new file
		// cannot be given the same inode number (a filesystem may reuse it)
		if err := os.Link(file, file+".link"); err != nil {
			t.Fatal(err)
		}
		os.Remove(file)
		put(t, file, 50, old)
		clean(t, f.sc)
		if len(f.trashed) != 0 {
			t.Fatalf("trashed = %v, want the new file kept", f.trashed)
		}
	})
	t.Run("file opened after the scan", func(t *testing.T) {
		f := newFixture(t)
		oldFolder(t, f, "dir")
		keep := put(t, f.p("Downloads", "opened.dmg"), 50, old)
		scanned(t, f)
		at(t, keep, time.Minute, old)
		res := clean(t, f.sc)
		if len(f.trashed) != 1 || res.Failed != 1 || !slices.ContainsFunc(res.Skipped, func(s string) bool { return strings.Contains(s, "since the scan") }) {
			t.Fatalf("trashed = %v res = %+v, want only the folder", f.trashed, res)
		}
	})
	t.Run("file inside a folder opened after the scan", func(t *testing.T) {
		f := newFixture(t)
		folder := oldFolder(t, f, "dir")
		scanned(t, f)
		at(t, filepath.Join(folder, "a.bin"), time.Minute, old)
		clean(t, f.sc)
		if len(f.trashed) != 0 {
			t.Fatalf("trashed = %v, want the folder kept", f.trashed)
		}
	})
	t.Run("file added to a folder after the scan", func(t *testing.T) {
		f := newFixture(t)
		folder := oldFolder(t, f, "dir")
		scanned(t, f)
		put(t, filepath.Join(folder, "new.bin"), 5, time.Minute)
		clean(t, f.sc)
		if len(f.trashed) != 0 {
			t.Fatalf("trashed = %v, want the folder kept", f.trashed)
		}
	})
	t.Run("entry that was not in the scan", func(t *testing.T) {
		f := newFixture(t)
		oldFolder(t, f, "dir")
		scanned(t, f)
		put(t, f.p("Downloads", "late.dmg"), 5, old)
		clean(t, f.sc)
		if len(f.trashed) != 1 || filepath.Base(f.trashed[0]) != "dir" {
			t.Fatalf("trashed = %v, want only the scanned folder", f.trashed)
		}
	})
	t.Run("entry gone since the scan", func(t *testing.T) {
		f := newFixture(t)
		folder := oldFolder(t, f, "dir")
		scanned(t, f)
		os.RemoveAll(folder)
		if res := clean(t, f.sc); len(f.trashed) != 0 || res.Failed != 0 {
			t.Fatalf("trashed = %v res = %+v", f.trashed, res)
		}
	})
	t.Run("an empty scan leaves everything", func(t *testing.T) {
		f := newFixture(t)
		oldFolder(t, f, "dir")
		f.sc.Downloads = map[string]snapshot.Seen{}
		clean(t, f.sc)
		if len(f.trashed) != 0 {
			t.Fatalf("trashed = %v", f.trashed)
		}
	})
}

// A mount point appearing in a scanned folder is still caught at clean time.
func TestCleanKeepsScannedFolderThatNowHoldsAMount(t *testing.T) {
	f := newFixture(t)
	folder := oldFolder(t, f, "dir")
	put(t, filepath.Join(folder, "mnt", "b.bin"), 5, old)
	ageDirs(t, f.p("Downloads"))
	scanned(t, f)
	mnt := filepath.Join(folder, "mnt")
	was := dirDev
	dirDev = func(p string, fi fs.FileInfo) uint64 {
		if p == mnt {
			return was(p, fi) + 1
		}
		return was(p, fi)
	}
	t.Cleanup(func() { dirDev = was })
	if res := clean(t, f.sc); len(f.trashed) != 0 || res.Failed != 1 {
		t.Fatalf("trashed = %v res = %+v", f.trashed, res)
	}
}

// The macOS "last used" date counts for top-level files, is asked in one
// batch, and a failed lookup keeps the files.
func TestLastUsedDateKeepsTopLevelFiles(t *testing.T) {
	setup := func(t *testing.T) (*fixture, string, string, string) {
		f := newFixture(t)
		used := put(t, f.p("Downloads", "used.dmg"), 50, old)
		unused := put(t, f.p("Downloads", "unused.dmg"), 50, old)
		folder := oldFolder(t, f, "dir")
		return f, used, unused, folder
	}
	t.Run("recently used file is kept", func(t *testing.T) {
		f, used, unused, folder := setup(t)
		var mu sync.Mutex
		var asked [][]string
		f.sc.LastUsed = func(_ context.Context, paths []string) (map[string]int64, error) {
			mu.Lock()
			asked = append(asked, slices.Clone(paths))
			mu.Unlock()
			return map[string]int64{used: time.Now().Add(-5 * 24 * time.Hour).UnixMilli()}, nil
		}
		r := f.sc.findOldDownloads(context.Background())
		slices.Sort(r.paths)
		want := []string{folder, unused}
		slices.Sort(want)
		if !slices.Equal(r.paths, want) {
			t.Fatalf("paths = %v, want %v", r.paths, want)
		}
		if len(asked) != 1 {
			t.Fatalf("asked %d times, want one batch", len(asked))
		}
		if !slices.Contains(asked[0], folder) {
			t.Fatal("the date of a folder was not asked for")
		}
	})
	t.Run("failed lookup keeps the files", func(t *testing.T) {
		f, _, _, _ := setup(t)
		f.sc.LastUsed = func(context.Context, []string) (map[string]int64, error) { return nil, errors.New("mdls failed") }
		r := f.sc.findOldDownloads(context.Background())
		if len(r.paths) != 0 {
			t.Fatalf("paths = %v, want nothing offered (files and folders are all kept)", r.paths)
		}
	})
	t.Run("opened between scan and clean", func(t *testing.T) {
		f, used, _, _ := setup(t)
		used = f.p("Downloads", "used.dmg")
		f.sc.LastUsed = func(context.Context, []string) (map[string]int64, error) { return nil, nil }
		scanned(t, f)
		f.sc.LastUsed = func(_ context.Context, paths []string) (map[string]int64, error) {
			return map[string]int64{used: time.Now().Add(-time.Hour).UnixMilli()}, nil
		}
		clean(t, f.sc)
		if slices.Contains(f.trashed, used) || len(f.trashed) != 2 {
			t.Fatalf("trashed = %v, want everything but the used file", f.trashed)
		}
	})
}

func mdlsOut(dates ...string) string { return strings.Join(dates, "\x00") }

func TestMdlsLastUsed(t *testing.T) {
	paths := make([]string, 120)
	for i := range paths {
		paths[i] = "/d/f" + strconv.Itoa(i)
	}
	var mu sync.Mutex
	calls := 0
	ok := sys.RunFunc(func(_ context.Context, name string, args ...string) (sys.Result, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if name != "/usr/bin/mdls" || !slices.Equal(args[:3], []string{"-raw", "-name", "kMDItemLastUsedDate"}) {
			t.Errorf("run %s %v", name, args)
		}
		out := make([]string, len(args)-3)
		for i := range out {
			out[i] = "(null)"
		}
		out[0] = "2026-10-19 16:41:26 +0000"
		return sys.Result{Stdout: mdlsOut(out...)}, nil
	})
	dates, err := MdlsLastUsed(ok)(context.Background(), paths)
	if err != nil || calls != 3 || len(dates) != 120 || dates["/d/f0"] == 0 || dates["/d/f1"] != 0 || dates["/d/f50"] == 0 {
		t.Fatalf("dates = %d entries, calls = %d, err = %v", len(dates), calls, err)
	}
	fail := func(res sys.Result, err error) func(context.Context, []string) (map[string]int64, error) {
		return MdlsLastUsed(sys.RunFunc(func(context.Context, string, ...string) (sys.Result, error) { return res, err }))
	}
	if _, err := fail(sys.Result{}, errors.New("boom"))(context.Background(), paths[:2]); err == nil {
		t.Error("a failed run must fail the lookup")
	}
	if _, err := fail(sys.Result{Stdout: mdlsOut("yesterday", "(null)")}, nil)(context.Background(), paths[:2]); err == nil {
		t.Error("an unreadable date must fail the lookup")
	}
	if _, err := fail(sys.Result{Stdout: "(null)"}, nil)(context.Background(), paths[:2]); err == nil {
		t.Error("a short output must fail the lookup")
	}
	if d, err := fail(sys.Result{}, &os.PathError{Op: "fork/exec", Path: "/usr/bin/mdls", Err: os.ErrNotExist})(context.Background(), paths[:2]); err != nil || len(d) != 0 {
		t.Errorf("no mdls (not a Mac) must give no dates and no error: %v, %v", d, err)
	}
	if MdlsLastUsed(nil) != nil {
		t.Error("nil runner must give no lookup")
	}
}

// An app bundle used from Downloads shows no sign of it inside (launching an
// app writes nothing to the bundle), only Spotlight knows: the folder is kept
// at scan and at clean.
func TestAppBundleLastUsedKeepsTheFolder(t *testing.T) {
	setup := func(t *testing.T) (*fixture, string) {
		f := newFixture(t)
		app := f.p("Downloads", "Tool.app")
		file := put(t, filepath.Join(app, "Contents", "MacOS", "Tool"), 100, old)
		at(t, file, old, old)
		ageDirs(t, f.p("Downloads"))
		return f, app
	}
	recent := func(app string) func(context.Context, []string) (map[string]int64, error) {
		return func(_ context.Context, paths []string) (map[string]int64, error) {
			return map[string]int64{app: time.Now().Add(-3 * 24 * time.Hour).UnixMilli()}, nil
		}
	}
	t.Run("kept at scan", func(t *testing.T) {
		f, app := setup(t)
		f.sc.LastUsed = recent(app)
		if r := f.sc.findOldDownloads(context.Background()); len(r.paths) != 0 {
			t.Fatalf("paths = %v, want the app kept", r.paths)
		}
	})
	t.Run("never used is offered", func(t *testing.T) {
		f, app := setup(t)
		f.sc.LastUsed = func(context.Context, []string) (map[string]int64, error) { return nil, nil }
		if r := f.sc.findOldDownloads(context.Background()); !slices.Equal(r.paths, []string{app}) {
			t.Fatalf("paths = %v, want the app", r.paths)
		}
	})
	t.Run("lookup failure keeps it", func(t *testing.T) {
		f, _ := setup(t)
		f.sc.LastUsed = func(context.Context, []string) (map[string]int64, error) { return nil, errors.New("mdls failed") }
		if r := f.sc.findOldDownloads(context.Background()); len(r.paths) != 0 {
			t.Fatalf("paths = %v", r.paths)
		}
	})
	t.Run("launched between scan and clean", func(t *testing.T) {
		f, app := setup(t)
		f.sc.LastUsed = func(context.Context, []string) (map[string]int64, error) { return nil, nil }
		scanned(t, f)
		f.sc.LastUsed = recent(app)
		clean(t, f.sc)
		if len(f.trashed) != 0 || !exists(app) {
			t.Fatalf("trashed = %v, want the app kept", f.trashed)
		}
	})
}
