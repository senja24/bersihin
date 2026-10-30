package junk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A parent of the junk root is swapped for a link after the roots were chosen.
func TestParentOfRootSwappedForLink(t *testing.T) {
	f := newFixture(t)
	precious := filepath.Join(f.outside, "precious", "Chrome")
	put(t, filepath.Join(precious, "mail.db"), 10, old)
	google := f.p("Library", "Caches", "Google")
	root := filepath.Join(google, "Chrome")
	os.MkdirAll(root, 0o755)
	// Defs() accepted the root; now the attacker swaps its parent
	os.RemoveAll(google)
	os.Symlink(filepath.Join(f.outside, "precious"), google)

	out := DeleteEntry(context.Background(), filepath.Join(root, "mail.db"), root, DeleteOptions{Home: f.home})
	if out.Removed != 0 || !out.Refused {
		t.Fatalf("out = %+v", out)
	}
	if !exists(filepath.Join(precious, "mail.db")) {
		t.Fatal("deleted a file outside the junk root")
	}
	// and the folder variant
	put(t, filepath.Join(precious, "sub", "x"), 1, old)
	out = DeleteEntry(context.Background(), filepath.Join(root, "sub"), root, DeleteOptions{Home: f.home})
	if out.Removed != 0 || !exists(filepath.Join(precious, "sub", "x")) {
		t.Fatalf("folder variant: %+v", out)
	}
}

func TestDeleteNeedsHome(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	a := put2(t, filepath.Join(root, "app", "a"))
	for _, home := range []string{"", f.outside, filepath.Join(f.home, "Library", "Caches"), "/"} {
		out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: home})
		if !out.Refused || !exists(a) {
			t.Errorf("home %q: out = %+v", home, out)
		}
	}
}

func put2(t *testing.T, p string) string { t.Helper(); put(t, p, 1, old); return p }

// A folder below the root is swapped for a link between the scan and the delete.
func TestFolderSwappedMidDelete(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "sub", "a.bin"), 5, old)
	put(t, filepath.Join(root, "app", "b.bin"), 5, old)
	victim := filepath.Join(f.outside, "victim")
	put(t, filepath.Join(victim, "a.bin"), 5, old) // same name as the file in the swapped folder

	afterScan = func() {
		sub := filepath.Join(root, "app", "sub")
		os.RemoveAll(sub)
		if err := os.Symlink(victim, sub); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { afterScan = func() {} }()

	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if !exists(filepath.Join(victim, "a.bin")) {
		t.Fatalf("a swapped folder redirected the deletion: %+v", out)
	}
	if exists(filepath.Join(root, "app", "b.bin")) {
		t.Error("the honest file should still be deleted")
	}
}

// The root itself is swapped for a link between the scan and the delete.
func TestRootSwappedMidDelete(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "a.bin"), 5, old)
	victim := filepath.Join(f.outside, "victim")
	put(t, filepath.Join(victim, "app", "a.bin"), 5, old)
	moved := f.p("Library", "Caches.orig")

	afterScan = func() {
		os.Rename(root, moved)
		os.Symlink(victim, root)
	}
	defer func() { afterScan = func() {} }()
	DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if !exists(filepath.Join(victim, "app", "a.bin")) {
		t.Fatal("deleted through a swapped root")
	}
}

func TestDeleteStopsOnCancel(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	for i := range 200 {
		put(t, filepath.Join(root, "app", "d", string(rune('a'+i%26))+string(rune('a'+i/26))), 1, old)
	}
	ctx, cancel := context.WithCancel(context.Background())
	afterScan = cancel
	defer func() { afterScan = func() {} }()
	out := DeleteEntry(ctx, filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Removed >= 200 {
		t.Fatalf("kept deleting after cancel: %+v", out)
	}
}

func TestDeleteSkipsNewestFolders(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "fresh", "x"), 1, 48*time.Hour)
	// the folder is brand new even though its file is old
	opt := DeleteOptions{Home: f.home, ModifiedBefore: time.Now().Add(-24 * time.Hour)}
	DeleteEntry(context.Background(), filepath.Join(root, "app"), root, opt)
	if !exists(filepath.Join(root, "app", "fresh")) || exists(filepath.Join(root, "app", "fresh", "x")) {
		t.Error("fresh folder should stay, its old file go")
	}
}

// A folder below the entry is swapped for a link to a sibling inside the same
// root (an excluded keep-cache): single-name handles must not follow it.
func TestInRootLinkCannotReachExcludedSibling(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "sub", "state.db"), 5, old)
	put(t, filepath.Join(root, "CloudKit", "state.db"), 5, old)
	afterScan = func() {
		sub := filepath.Join(root, "app", "sub")
		os.RemoveAll(sub)
		os.Symlink("../CloudKit", sub)
	}
	defer func() { afterScan = func() {} }()
	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if !exists(filepath.Join(root, "CloudKit", "state.db")) {
		t.Fatalf("followed an in-root link into CloudKit: %+v", out)
	}
	if out.Removed != 0 {
		t.Errorf("out = %+v", out)
	}
}

func TestDeepTargetThroughLinkedMiddleIsRefused(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "real", "deep", "x"), 1, old)
	os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias"))
	out := DeleteEntry(context.Background(), filepath.Join(root, "alias", "deep"), root, DeleteOptions{Home: f.home})
	if !out.Refused || !exists(filepath.Join(root, "real", "deep", "x")) {
		t.Fatalf("out = %+v", out)
	}
	// a deep target reached honestly works
	out = DeleteEntry(context.Background(), filepath.Join(root, "real", "deep"), root, DeleteOptions{Home: f.home})
	if out.Removed != 1 || exists(filepath.Join(root, "real", "deep")) {
		t.Fatalf("honest deep target: %+v", out)
	}
}

func TestLinkAsTargetIsRefused(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(f.outside, "d", "x"), 1, old)
	os.MkdirAll(root, 0o755)
	os.Symlink(filepath.Join(f.outside, "d"), filepath.Join(root, "l"))
	if out := DeleteEntry(context.Background(), filepath.Join(root, "l"), root, DeleteOptions{Home: f.home}); !out.Refused || !exists(filepath.Join(f.outside, "d", "x")) {
		t.Fatalf("out = %+v", out)
	}
}

func TestDevOfRegularFolderIsStable(t *testing.T) {
	// the device check must not trip on an ordinary tree (all one device)
	f := newFixture(t)
	root := f.p("Library", "Caches")
	put(t, filepath.Join(root, "app", "a", "b", "c"), 1, old)
	out := DeleteEntry(context.Background(), filepath.Join(root, "app"), root, DeleteOptions{Home: f.home})
	if out.Refused || out.Removed != 1 || exists(filepath.Join(root, "app")) {
		t.Fatalf("out = %+v", out)
	}
}

// A hard link's data stays on disk while another name points at it, so only
// the removal of the last name counts as freed bytes.
func TestHardLinkedBytesAreNotCountedAsFreed(t *testing.T) {
	f := newFixture(t)
	root := f.p("Library", "Caches")
	keep := put(t, filepath.Join(f.outside, "keep.bin"), 1000, old)
	os.MkdirAll(root, 0o755)
	if err := os.Link(keep, filepath.Join(root, "linked.bin")); err != nil {
		t.Skip("hard links unavailable:", err)
	}
	put(t, filepath.Join(root, "plain.bin"), 300, old)

	// shared with another name outside: removed, nothing freed
	out := DeleteEntry(context.Background(), filepath.Join(root, "linked.bin"), root, DeleteOptions{Home: f.home})
	if out.Removed != 1 || out.Freed != 0 {
		t.Fatalf("linked: %+v", out)
	}
	if !exists(keep) {
		t.Fatal("the other name was removed")
	}
	out = DeleteEntry(context.Background(), filepath.Join(root, "plain.bin"), root, DeleteOptions{Home: f.home})
	if out.Removed != 1 || out.Freed != 300 {
		t.Fatalf("plain: %+v", out)
	}

	// two names inside one tree: the data is freed once, with the last name
	tree := filepath.Join(root, "tree")
	a := put(t, filepath.Join(tree, "a.bin"), 700, old)
	if err := os.Link(a, filepath.Join(tree, "b.bin")); err != nil {
		t.Fatal(err)
	}
	out = DeleteEntry(context.Background(), tree, root, DeleteOptions{Home: f.home})
	if out.Removed != 2 || out.Freed != 700 {
		t.Fatalf("tree: %+v", out)
	}
}

// The size a scan shows is what a clean frees: hard-linked data counts once
// when every name is inside the scan, and not at all while a name outside
// still points at it.
func TestScanCountsHardLinksLikeClean(t *testing.T) {
	f := newFixture(t)
	logs := f.p("Library", "Logs")
	pair := put(t, filepath.Join(logs, "app", "a.log"), 1000, old)
	if err := os.Link(pair, filepath.Join(logs, "app", "b.log")); err != nil {
		t.Skip("hard links unavailable:", err)
	}
	shared := put(t, filepath.Join(f.outside, "shared"), 4000, old)
	if err := os.Link(shared, filepath.Join(logs, "app", "shared.log")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(logs, "app", "plain.log"), 300, old)
	ageDirs(t, f.p("Library"))

	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil || len(cats) != 1 {
		t.Fatalf("scan = %+v, %v", cats, err)
	}
	res, err := f.sc.Clean(context.Background(), []string{"mac-logs"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cats[0].Size != 1300 || res.Freed != cats[0].Size {
		t.Fatalf("scan shows %d, clean freed %d, want both 1300", cats[0].Size, res.Freed)
	}
}
