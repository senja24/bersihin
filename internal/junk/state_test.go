package junk

import (
	"context"
	"testing"
	"time"
)

// Files of running apps are fresh: every cache and log category keeps what
// changed in the last day.
func TestCacheAndLogCategoriesKeepRecentFiles(t *testing.T) {
	for _, d := range Defs("/Users/alice") {
		switch d.ID {
		case "mac-caches", "mac-logs", "browser-cache", "xcode", "dev-cache":
			if d.KeepNewer < 24*time.Hour {
				t.Errorf("%s: KeepNewer = %v, want at least 24h", d.ID, d.KeepNewer)
			}
		}
	}
}

func TestCleanAndScanKeepFreshCacheFiles(t *testing.T) {
	f := newFixture(t)
	put(t, f.p("Library", "Caches", "com.foo.app", "stale"), 4000, old)
	put(t, f.p("Library", "Caches", "com.foo.app", "live.db"), 900, time.Minute)
	put(t, f.p("Library", "Caches", "com.fresh.app", "live"), 800, time.Hour)
	put(t, f.p("Library", "Logs", "stale.log"), 2000, old)
	put(t, f.p("Library", "Logs", "today.log"), 700, time.Minute)
	ageDirs(t, f.p("Library"))

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	for _, c := range cats {
		sizes[c.ID] = c.Size
	}
	if sizes["mac-caches"] != 4000 || sizes["mac-logs"] != 2000 {
		t.Errorf("scan counted fresh files: %v", sizes)
	}
	res, err := f.sc.Clean(context.Background(), []string{"mac-caches", "mac-logs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range [][]string{
		{"Library", "Caches", "com.foo.app", "live.db"}, {"Library", "Caches", "com.fresh.app", "live"}, {"Library", "Logs", "today.log"},
	} {
		if !exists(f.p(keep...)) {
			t.Errorf("fresh file %v was deleted", keep)
		}
	}
	if exists(f.p("Library", "Caches", "com.foo.app", "stale")) || exists(f.p("Library", "Logs", "stale.log")) {
		t.Error("stale files survived")
	}
	if res.Freed != 6000 {
		t.Errorf("freed = %d, want 6000", res.Freed)
	}
}

// Folders that hold real state (virtual environments, local history, Apple
// folders that are not pure caches) are never emptied.
func TestStateFoldersInCachesAreKept(t *testing.T) {
	f := newFixture(t)
	c := func(rel ...string) []string { return append([]string{"Library", "Caches"}, rel...) }
	kept := [][]string{
		c("pypoetry", "virtualenvs", "proj-AbC-py3.12", "lib", "site.py"),
		c("JetBrains", "IntelliJIdea2025.1", "LocalHistory", "changes.storageData"),
		c("com.apple.Maps", "state.db"),
		c("com.apple.Music", "library.db"),
		c("tool", ".venv", "bin", "python"),
		c("tool", "venv", "bin", "python"),
		c("tool", "MyVirtualEnv", "x"),
		c("tool", "work.venv"),
	}
	gone := [][]string{
		c("pypoetry", "cache", "artifacts", "a.whl"),
		c("JetBrains", "IntelliJIdea2025.1", "caches", "c.dat"),
		c("com.apple.python", "pycache", "x.pyc"),
		c("com.foo.app", "data"),
		c("tool", "other", "blob"),
	}
	for _, p := range append(append([][]string{}, kept...), gone...) {
		put(t, f.p(p...), 100, old)
	}
	wantSize := int64(100 * len(gone))
	ageDirs(t, f.p("Library"))

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	for _, cat := range cats {
		if cat.ID == "mac-caches" {
			got = cat.Size
		}
	}
	if got != wantSize {
		t.Errorf("scan counts %d bytes, want %d (state folders must not be offered)", got, wantSize)
	}
	if _, err := f.sc.Clean(context.Background(), []string{"mac-caches"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range kept {
		if !exists(f.p(p...)) {
			t.Errorf("state %v was deleted", p)
		}
	}
	for _, p := range gone {
		if exists(f.p(p...)) {
			t.Errorf("cache %v survived", p)
		}
	}
}

// Emptying the Trash removes everything, a trashed project's venv included:
// the state-folder rule is for caches only.
func TestTrashIgnoresStateFolderRule(t *testing.T) {
	f := newFixture(t)
	put(t, f.p(".Trash", "p", "venv", "x"), 100, old)
	put(t, f.p(".Trash", "p", ".venv", "bin", "python"), 100, old)
	put(t, f.p(".Trash", "p", "src.go"), 100, old)
	ageDirs(t, f.p(".Trash"))

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 1 || cats[0].ID != "trash" || cats[0].Size != 300 {
		t.Fatalf("scan = %+v, want the whole 300 bytes of the Trash", cats)
	}
	if _, err := f.sc.Clean(context.Background(), []string{"trash"}, nil); err != nil {
		t.Fatal(err)
	}
	if exists(f.p(".Trash", "p")) {
		t.Error("a trashed project survived emptying the Trash")
	}
	if !exists(f.p(".Trash")) {
		t.Error("the Trash folder itself must stay")
	}
}
