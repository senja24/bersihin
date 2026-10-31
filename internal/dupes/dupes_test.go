package dupes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/testenv"
	"dev.agus.bersihin/internal/walk"
)

const mib = 1024 * 1024

func content(seed byte, size int) []byte {
	b := bytes.Repeat([]byte{seed}, size)
	for i := 0; i < size; i += 4099 { // not constant, so a hole cannot make it sparse
		b[i] = byte(i)
	}
	return b
}

func write(t *testing.T, p string, b []byte, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func paths(files []Entry) []string {
	var out []string
	for _, f := range files {
		out = append(out, filepath.Base(f.Path))
	}
	slices.Sort(out)
	return out
}

func TestFindGroups(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	same := content(1, mib+10)
	write(t, filepath.Join(root, "a", "one.bin"), same, now.Add(-3*time.Hour))
	write(t, filepath.Join(root, "b", "two.bin"), same, now.Add(-1*time.Hour))
	write(t, filepath.Join(root, "c", "three.bin"), same, now.Add(-2*time.Hour))

	// same size, different content from the first byte
	write(t, filepath.Join(root, "diff1.bin"), content(2, mib+10), now)
	// same size, same head and tail, different middle: only the full hash tells them apart
	mid1, mid2 := content(3, 3*edge), content(3, 3*edge)
	mid2[edge+edge/2] ^= 0xff
	write(t, filepath.Join(root, "mid1.bin"), mid1, now)
	write(t, filepath.Join(root, "mid2.bin"), mid2, now)
	// big pair
	big := content(4, 5*mib)
	write(t, filepath.Join(root, "big1.bin"), big, now)
	write(t, filepath.Join(root, "big2.bin"), big, now)
	// too small, hidden, inside excluded folders
	small := content(5, 1000)
	write(t, filepath.Join(root, "small1"), small, now)
	write(t, filepath.Join(root, "small2"), small, now)
	hid := content(6, mib+1)
	write(t, filepath.Join(root, ".hidden1"), hid, now)
	write(t, filepath.Join(root, ".hiddendir", "x"), hid, now)
	write(t, filepath.Join(root, "node_modules", "pkg", "x.bin"), hid, now)
	write(t, filepath.Join(root, "My.photoslibrary", "originals", "x.heic"), hid, now)
	write(t, filepath.Join(root, "Foo.app", "Contents", "x"), hid, now)

	entries := Collect(context.Background(), []string{root}, nil)
	got := paths(entries)
	for _, bad := range []string{"small1", "small2", ".hidden1", "x", "x.bin", "x.heic"} {
		if slices.Contains(got, bad) {
			t.Errorf("%s should not be collected", bad)
		}
	}

	groups, err := Find(context.Background(), entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	// biggest waste first: the 5 MiB pair wastes 5 MiB, the triple wastes 2 * 1 MiB
	if groups[0].Size != 5*mib || len(groups[0].Files) != 2 {
		t.Errorf("first group = %+v", groups[0])
	}
	g := groups[1]
	if g.Size != mib+10 || len(g.Files) != 3 {
		t.Fatalf("second group = %+v", g)
	}
	// newest first
	var order []string
	for _, f := range g.Files {
		order = append(order, filepath.Base(f.Path))
	}
	if !slices.Equal(order, []string{"two.bin", "three.bin", "one.bin"}) {
		t.Errorf("order = %v", order)
	}
	if len(g.Hash) != 64 {
		t.Errorf("hash = %q", g.Hash)
	}
	for _, grp := range groups {
		for _, f := range grp.Files {
			if filepath.Base(f.Path) == "mid1.bin" || filepath.Base(f.Path) == "mid2.bin" || filepath.Base(f.Path) == "diff1.bin" {
				t.Errorf("%s is not a duplicate", f.Path)
			}
		}
	}
}

func TestPartialHashCollisionResolvedByFullHash(t *testing.T) {
	root := t.TempDir()
	a, b := content(9, 4*edge), content(9, 4*edge)
	b[2*edge] ^= 1
	write(t, filepath.Join(root, "a"), a, time.Now())
	write(t, filepath.Join(root, "b"), b, time.Now())
	ha, _ := PartialHash(filepath.Join(root, "a"), int64(len(a)))
	hb, _ := PartialHash(filepath.Join(root, "b"), int64(len(b)))
	if ha != hb {
		t.Fatal("test setup: partial hashes should collide")
	}
	fa, _ := FullHash(context.Background(), filepath.Join(root, "a"))
	fb, _ := FullHash(context.Background(), filepath.Join(root, "b"))
	if fa == fb {
		t.Fatal("full hashes must differ")
	}
}

func TestHardLinksAndLinksAreNotCopies(t *testing.T) {
	root := t.TempDir()
	data := content(7, mib+5)
	write(t, filepath.Join(root, "orig.bin"), data, time.Now())
	if err := os.Link(filepath.Join(root, "orig.bin"), filepath.Join(root, "hard.bin")); err != nil {
		t.Skip("hard links unavailable:", err)
	}
	testenv.Symlink(t, filepath.Join(root, "orig.bin"), filepath.Join(root, "soft.bin"))
	entries := Collect(context.Background(), []string{root, root}, nil) // the same root twice
	if len(entries) != 1 {
		t.Fatalf("entries = %v", paths(entries))
	}
	groups, _ := Find(context.Background(), entries, nil)
	if len(groups) != 0 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestFindCancelled(t *testing.T) {
	root := t.TempDir()
	d := content(8, mib+1)
	write(t, filepath.Join(root, "a"), d, time.Now())
	write(t, filepath.Join(root, "b"), d, time.Now())
	entries := Collect(context.Background(), []string{root}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Find(ctx, entries, nil); err != progress.ErrCancelled {
		t.Fatalf("err = %v", err)
	}
}

func TestFindReportsProgress(t *testing.T) {
	root := t.TempDir()
	d := content(8, mib+1)
	write(t, filepath.Join(root, "a"), d, time.Now())
	write(t, filepath.Join(root, "b"), d, time.Now())
	var phases []string
	var mu sync.Mutex
	rep := progress.Reporter(func(phase string, done, total int, _ string) {
		mu.Lock()
		defer mu.Unlock()
		if len(phases) == 0 || phases[len(phases)-1] != phase {
			phases = append(phases, phase)
		}
		if total != 2 || done < 1 || done > 2 {
			t.Errorf("progress %s %d/%d", phase, done, total)
		}
	})
	if _, err := Find(context.Background(), Collect(context.Background(), []string{root}, nil), rep); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phases, []string{"Comparing files", "Checking contents"}) {
		t.Fatalf("phases = %v", phases)
	}
}

func TestRoots(t *testing.T) {
	testenv.MacPaths(t)
	got := Roots("/Users/a")
	want := []string{"/Users/a/Desktop", "/Users/a/Documents", "/Users/a/Downloads", "/Users/a/Pictures", "/Users/a/Movies", "/Users/a/Music"}
	if !slices.Equal(got, want) {
		t.Fatalf("roots = %v", got)
	}
}

// Runs the real path (Roots, then Collect) over a home that holds big files in
// Library: none of them may be offered as duplicates.
func TestRootsNeverIncludeLibrary(t *testing.T) {
	home := t.TempDir()
	big := make([]byte, 2*MinSize)
	for _, p := range []string{
		filepath.Join(home, "Library", "Containers", "x", "a.bin"),
		filepath.Join(home, "Library", "Application Support", "b.bin"),
		filepath.Join(home, "Documents", "keep.bin"),
		filepath.Join(home, "Documents", "Library", "inner.bin"), // a user's own folder called Library
	} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, big, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := filepath.Join(home, "Library") + string(filepath.Separator)
	for _, r := range Roots(home) {
		if strings.HasPrefix(r+string(filepath.Separator), lib) {
			t.Errorf("Roots includes %q", r)
		}
	}
	got := Collect(context.Background(), Roots(home), nil)
	for _, e := range got {
		if strings.HasPrefix(e.Path, lib) {
			t.Errorf("Collect offered %q from ~/Library", e.Path)
		}
	}
	if len(got) == 0 {
		t.Fatal("nothing collected from Documents")
	}
}

func TestEligibleSkipsPlaceholders(t *testing.T) {
	big := int64(5 * MinSize)
	tests := []struct {
		name string
		f    walk.File
		want bool
	}{
		{"real file", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true, Blocks: 100}}, true},
		{"no blocks", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true}}, false},
		{"flagged dataless with blocks", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true, Blocks: 100, Dataless: true}}, false},
		{"small", walk.File{Name: "a.mov", Size: 10}, false},
	}
	for _, tc := range tests {
		if got := eligible(tc.f); got != tc.want {
			t.Errorf("%s: eligible = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFullHashRegularRefusesLinksAndFolders(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.WriteFile(real, []byte("content"), 0o644)
	link := filepath.Join(dir, "link")
	testenv.Symlink(t, real, link)
	want, err := FullHash(context.Background(), real)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := FullHashRegular(context.Background(), real); err != nil || got != want {
		t.Fatalf("regular file: %q %v", got, err)
	}
	if _, err := FullHashRegular(context.Background(), link); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("link: %v", err)
	}
	if _, err := FullHashRegular(context.Background(), dir); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("folder: %v", err)
	}
	if _, err := FullHashRegular(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file hashed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FullHashRegular(ctx, real); err == nil {
		t.Fatal("cancelled context ignored")
	}
}

func TestCollectSkipsMorePackages(t *testing.T) {
	root := t.TempDir()
	hid := bytes.Repeat([]byte("x"), MinSize+1)
	for _, d := range []string{"a.sparseimage", "b.xcarchive"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "big.bin"), hid, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Collect(context.Background(), []string{root}, nil); len(got) != 0 {
		t.Fatalf("entered a package: %+v", got)
	}
}
