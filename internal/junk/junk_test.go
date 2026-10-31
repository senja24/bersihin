package junk

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/trash"
)

func put(t *testing.T, p string, size int, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

type fixture struct {
	home, outside string
	trashed       []string
	sc            *Scanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{home: filepath.Join(base, "home", "alice"), outside: filepath.Join(base, "outside")}
	os.MkdirAll(f.home, 0o755)
	os.MkdirAll(f.outside, 0o755)
	f.sc = &Scanner{
		Home:     f.home,
		Platform: "darwin", // the Mac's categories, on any OS
		Trasher: &trash.Trasher{
			Guard: func(context.Context) pathsafe.Guard {
				g := pathsafe.NewGuard(f.home, nil)
				g.SystemRoots = nil
				return g
			},
			Move: func(p string) error { f.trashed = append(f.trashed, p); return nil },
		},
		// a test cannot set a status change time back; those that look at it say so
		ignoreCtime: true,
	}
	return f
}

func (f *fixture) p(rel ...string) string { return filepath.Join(append([]string{f.home}, rel...)...) }

const old = 200 * 24 * time.Hour

func TestDeleteEntryTree(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "com.foo", "a", "x.bin"), 100, old)
	put(t, filepath.Join(root, "com.foo", "y.bin"), 50, old)
	put(t, filepath.Join(f.outside, "precious"), 10, old)
	// a link inside the tree to a folder outside, and one to a file outside
	os.Symlink(f.outside, filepath.Join(root, "com.foo", "a", "linkdir"))
	os.Symlink(filepath.Join(f.outside, "precious"), filepath.Join(root, "com.foo", "linkfile"))

	out := DeleteEntry(context.Background(), filepath.Join(root, "com.foo"), root, DeleteOptions{Home: f.home})
	if out.Refused || out.Removed != 2 || out.Freed != 150 {
		t.Fatalf("out = %+v", out)
	}
	if !exists(filepath.Join(f.outside, "precious")) {
		t.Fatal("deleted through a link")
	}
	// links stay, so their folders stay
	if !exists(filepath.Join(root, "com.foo", "a", "linkdir")) {
		t.Error("link inside the tree was removed or followed")
	}
	if !exists(root) {
		t.Error("the root itself must stay")
	}
}

func TestDeleteEntryPlainFolderIsRemoved(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Logs")
	put(t, filepath.Join(root, "app", "sub", "l.log"), 10, old)
	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Removed != 1 || exists(filepath.Join(root, "app")) {
		t.Fatalf("out = %+v", out)
	}
}

func TestDeleteEntryRefuses(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "ok", "a"), 1, old)
	put(t, filepath.Join(f.outside, "dir", "secret"), 5, old)
	put(t, filepath.Join(f.home, "Documents", "doc.txt"), 5, old)
	os.Symlink(filepath.Join(f.outside, "dir"), filepath.Join(root, "evil"))
	os.Symlink(f.outside, filepath.Join(f.home, "Library", "rootlink"))

	tests := []struct {
		name, target, root string
	}{
		{"link as target", filepath.Join(root, "evil"), root},
		{"through link", filepath.Join(root, "evil", "secret"), root},
		{"root itself", root, root},
		{"outside the root", filepath.Join(f.home, "Documents", "doc.txt"), root},
		{"outside the root, other folder", filepath.Join(f.outside, "dir"), root},
		{"home", f.home, root},
		{"dotdot", root + "/ok/../../../Documents/doc.txt", root},
		{"root is a link", filepath.Join(f.home, "Library", "rootlink", "dir"), filepath.Join(f.home, "Library", "rootlink")},
		{"relative", "ok", root},
		{"empty", "", root},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := DeleteEntry(context.Background(), tc.target, tc.root, DeleteOptions{Home: f.home})
			if !out.Refused || out.Removed != 0 {
				t.Fatalf("out = %+v", out)
			}
		})
	}
	for _, p := range []string{
		filepath.Join(f.outside, "dir", "secret"), filepath.Join(f.home, "Documents", "doc.txt"), filepath.Join(root, "ok", "a"),
	} {
		if !exists(p) {
			t.Errorf("%s was deleted", p)
		}
	}
}

func TestDeleteEntryKeepsFreshFiles(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "old.bin"), 10, 48*time.Hour)
	put(t, filepath.Join(root, "app", "new.bin"), 20, time.Minute)
	put(t, filepath.Join(root, "single-old"), 5, 48*time.Hour)
	put(t, filepath.Join(root, "single-new"), 5, time.Minute)
	cut := DeleteOptions{Home: f.home, ModifiedBefore: time.Now().Add(-24 * time.Hour)}

	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, cut)
	if out.Removed != 1 || out.Freed != 10 {
		t.Fatalf("out = %+v", out)
	}
	if !exists(filepath.Join(root, "app", "new.bin")) || !exists(filepath.Join(root, "app")) {
		t.Error("fresh file or its folder was removed")
	}
	if DeleteEntry(context.Background(), filepath.Join(root, "single-old"), root, cut).Removed != 1 || exists(filepath.Join(root, "single-old")) {
		t.Error("old single file kept")
	}
	if DeleteEntry(context.Background(), filepath.Join(root, "single-new"), root, cut).Removed != 0 || !exists(filepath.Join(root, "single-new")) {
		t.Error("fresh single file removed")
	}
}

func TestDeleteEntryCancel(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "a"), 1, old)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := DeleteEntry(ctx, filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Removed != 0 || !exists(filepath.Join(root, "app", "a")) {
		t.Fatalf("deleted after cancel: %+v", out)
	}
}

func TestDefsIgnoreLinkedRoots(t *testing.T) {
	f := newFixture(t)
	put(t, filepath.Join(f.outside, "cache", "big"), 1000, old)
	os.MkdirAll(f.p("Library"), 0o755)
	os.Symlink(filepath.Join(f.outside, "cache"), f.p("Library", "Caches"))
	os.Symlink(f.outside, f.p("Library", "Logs"))

	for _, d := range Defs(f.home) {
		if d.Roots == nil {
			continue
		}
		if r := d.Roots(); len(r) != 0 {
			t.Errorf("%s: linked roots accepted: %+v", d.ID, r)
		}
	}
	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil || len(cats) != 0 {
		t.Fatalf("scan through links found %+v, %v", cats, err)
	}
	if _, err := f.sc.Clean(context.Background(), []string{"mac-caches", "mac-logs"}, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(f.outside, "cache", "big")) {
		t.Fatal("cleaned through a linked root")
	}
}

func populate(t *testing.T, f *fixture) {
	put(t, f.p("Library", "Caches", "com.foo.app", "data"), 4000, old)
	put(t, f.p("Library", "Caches", "com.apple.bird", "sync"), 9999, old) // sync state: excluded
	put(t, f.p("Library", "Caches", "Yarn", "pkg"), 7777, old)            // dev cache: its own category
	put(t, f.p("Library", "Caches", "Google", "Chrome", "Default", "Cache", "f_1"), 3000, old)
	put(t, f.p("Library", "Logs", "app.log"), 2000, old)
	put(t, f.p("Library", "Application Support", "Google", "Chrome", "Default", "Code Cache", "js", "c"), 1500, old)
	put(t, f.p("Library", "Application Support", "Google", "Chrome", "Default", "Cookies"), 800, old) // not a cache
	put(t, f.p("Library", "Developer", "Xcode", "DerivedData", "Proj-abc", "build.o"), 6000, old)
	put(t, f.p(".Trash", "deleted.zip"), 500, old)
	put(t, f.p("Downloads", "ancient.dmg"), 1200, old)
	put(t, f.p("Downloads", "recent.zip"), 900, time.Hour)
	put(t, f.p("Downloads", "mixed", "old.bin"), 100, old)
	put(t, f.p("Downloads", "mixed", "new.bin"), 100, time.Hour)
	put(t, f.p("Downloads", "oldfolder", "a.bin"), 300, old)
	put(t, f.p("Downloads", ".DS_Store"), 10, old)
	if err := os.Chtimes(f.p("Downloads", "oldfolder"), time.Now().Add(-old), time.Now().Add(-old)); err != nil {
		t.Fatal(err)
	}
	// Cache folders were last changed long ago too (a folder changed within
	// the last day is kept even once emptied).
	for _, root := range []string{f.p("Library"), f.p(".Trash")} {
		ageDirs(t, root)
	}
}

func ageDirs(t *testing.T, root string) {
	t.Helper()
	ts := time.Now().Add(-old)
	var dirs []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- { // children first, so a parent keeps its time
		os.Chtimes(dirs[i], ts, ts)
	}
}

func TestScan(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	var phases []string
	rep := progress.Reporter(func(phase string, _, _ int, _ string) {
		if len(phases) == 0 || phases[len(phases)-1] != phase {
			phases = append(phases, phase)
		}
	})
	cats, err := f.sc.Scan(context.Background(), rep)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.JunkCategory{}
	for _, c := range cats {
		got[c.ID] = c
	}
	want := map[string]struct {
		size  int64
		count int
		loc   string
		safe  bool
	}{
		"mac-caches":    {4000, 1, "~/Library/Caches", true},
		"mac-logs":      {2000, 1, "~/Library/Logs", true},
		"browser-cache": {3000 + 1500, 2, "", true}, // Caches/Google/Chrome and Chrome/Default/Code Cache
		"xcode":         {6000, 1, "~/Library/Developer/Xcode/DerivedData", false},
		"dev-cache":     {7777, 1, "~/Library/Caches/Yarn", false},
		"trash":         {500, 1, "~/.Trash", false},
		"old-downloads": {1200 + 300, 2, "~/Downloads", false},
	}
	if len(got) != len(want) {
		t.Fatalf("categories = %v", cats)
	}
	for id, w := range want {
		c, ok := got[id]
		if !ok {
			t.Errorf("%s missing", id)
			continue
		}
		if c.Size != w.size || c.Count != w.count || c.Safe != w.safe {
			t.Errorf("%s = size %d count %d safe %v, want %+v", id, c.Size, c.Count, c.Safe, w)
		}
		if w.loc != "" && !slices.Contains(c.Locations, w.loc) {
			t.Errorf("%s locations = %v, want %s", id, c.Locations, w.loc)
		}
	}
	if !slices.IsSortedFunc(cats, func(a, b model.JunkCategory) int { return int(b.Size - a.Size) }) {
		t.Errorf("not sorted by size: %v", cats)
	}
	if len(phases) < 2 || phases[len(phases)-1] != "Done" || phases[0] != "User caches" {
		t.Errorf("phases = %v", phases)
	}
}

func TestOldDownloadsIsNotOffered(t *testing.T) {
	// everything in Downloads is protected from the old-downloads rule when recent
	f := newFixture(t)
	put(t, f.p("Downloads", "recent.zip"), 900, time.Hour)
	cats, _ := f.sc.Scan(context.Background(), nil)
	if len(cats) != 0 {
		t.Fatalf("got %+v", cats)
	}
}

func TestClean(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	res, err := f.sc.Clean(context.Background(), []string{"mac-caches", "trash", "old-downloads", "nope", "../../etc", "mac-logs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exists(f.p("Library", "Caches", "com.foo.app")) || exists(f.p(".Trash", "deleted.zip")) || exists(f.p("Library", "Logs", "app.log")) {
		t.Error("cache entries survived")
	}
	for _, keep := range [][]string{
		{"Library", "Caches", "com.apple.bird", "sync"},                      // excluded
		{"Library", "Caches", "Yarn", "pkg"},                                 // not selected
		{"Library", "Caches", "Google", "Chrome", "Default", "Cache", "f_1"}, // browser category not selected
		{"Library", "Application Support", "Google", "Chrome", "Default", "Cookies"},
		{"Library", "Developer", "Xcode", "DerivedData", "Proj-abc", "build.o"},
		{"Downloads", "recent.zip"},
		{"Downloads", "mixed", "new.bin"},
	} {
		if !exists(f.p(keep...)) {
			t.Errorf("%v was removed", keep)
		}
	}
	for _, dir := range []string{"Library/Caches", ".Trash", "Library/Logs"} {
		if !exists(f.p(dir)) {
			t.Errorf("root %s was removed", dir)
		}
	}
	// old downloads are only moved to the Trash, never deleted
	wantTrashed := []string{f.p("Downloads", "ancient.dmg"), f.p("Downloads", "mixed", "old.bin"), f.p("Downloads", "oldfolder")}
	wantTrashed = wantTrashed[:0]
	wantTrashed = append(wantTrashed, f.p("Downloads", "ancient.dmg"), f.p("Downloads", "oldfolder"))
	slices.Sort(f.trashed)
	if !slices.Equal(f.trashed, wantTrashed) {
		t.Fatalf("trashed = %v, want %v", f.trashed, wantTrashed)
	}
	if !exists(f.p("Downloads", "ancient.dmg")) {
		t.Error("old downloads must not be deleted by the cleaner itself")
	}
	if res.Freed != 4000+500+2000+1200+300 || res.Failed != 0 {
		t.Errorf("res = %+v", res)
	}
	if !slices.Contains(res.Notes, "Old downloads went to the Trash, so you can restore them") {
		t.Errorf("notes = %v", res.Notes)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestCleanCancelled(t *testing.T) {
	f := newFixture(t)
	populate(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := f.sc.Clean(ctx, []string{"mac-caches"}, nil)
	if err != nil || res.Removed != 0 || !exists(f.p("Library", "Caches", "com.foo.app", "data")) {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := f.sc.Scan(ctx, nil); err != progress.ErrCancelled {
		t.Fatalf("scan err = %v", err)
	}
}

func TestFreshFilesKeptWhenDefHasKeepNewer(t *testing.T) {
	f := newFixture(t)
	root := f.p("tmp-root")
	put(t, filepath.Join(root, "old", "a"), 100, 48*time.Hour)
	put(t, filepath.Join(root, "new", "b"), 100, time.Minute)
	def := Def{ID: "x", Name: "x", KeepNewer: 24 * time.Hour}
	m := f.sc.measureCache(context.Background(), def, []Root{{Path: root}}, func(string) {})
	if m.size != 100 || m.count != 1 {
		t.Fatalf("measured %+v, want only the old file", m)
	}
}
