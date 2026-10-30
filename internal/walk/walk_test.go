package walk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func write(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkCountsAndDepth(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), 10)
	write(t, filepath.Join(root, "d1", "b.txt"), 20)
	write(t, filepath.Join(root, "d1", "d2", "c.txt"), 30)
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	depths := map[string]int{}
	var dirs []string
	st := Walk(context.Background(), root, Options{
		OnFile: func(f File) { depths[f.Name] = f.Depth },
		OnDir:  func(p string, _ int) { dirs = append(dirs, filepath.Base(p)) },
	})
	if st.Bytes != 60 || st.Files != 3 || st.Dirs != 3 {
		t.Fatalf("stats = %+v", st)
	}
	want := map[string]int{"a.txt": 1, "b.txt": 2, "c.txt": 3}
	for k, v := range want {
		if depths[k] != v {
			t.Errorf("depth of %s = %d, want %d", k, depths[k], v)
		}
	}
	slices.Sort(dirs)
	if !slices.Equal(dirs, []string{"d1", "d2", "empty"}) {
		t.Errorf("dirs = %v", dirs)
	}
}

func TestWalkNeverFollowsSymlinks(t *testing.T) {
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.bin"), 1000)
	root := t.TempDir()
	write(t, filepath.Join(root, "real.txt"), 5)
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.bin"), filepath.Join(root, "linkfile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "broken")); err != nil {
		t.Fatal(err)
	}

	var seen []string
	st := Walk(context.Background(), root, Options{OnFile: func(f File) { seen = append(seen, f.Name) }})
	if st.Bytes != 5 || !slices.Equal(seen, []string{"real.txt"}) {
		t.Fatalf("bytes=%d seen=%v", st.Bytes, seen)
	}
	if st.Dirs != 0 {
		t.Fatalf("followed a link: %+v", st)
	}
}

// A root that is itself a link (a symlinked home) is resolved once; links
// below it are still never followed. Paths keep the spelling of the root given.
func TestWalkSymlinkedRootIsResolvedOnce(t *testing.T) {
	target := t.TempDir()
	write(t, filepath.Join(target, "x"), 5)
	write(t, filepath.Join(target, "sub", "y"), 7)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret"), 100)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "escape")); err != nil {
		t.Fatal(err)
	}
	var seen, dirs []string
	st := Walk(context.Background(), link, Options{
		OnFile: func(f File) { seen = append(seen, f.Path) },
		OnDir:  func(p string, _ int) { dirs = append(dirs, p) },
	})
	slices.Sort(seen)
	want := []string{filepath.Join(link, "sub", "y"), filepath.Join(link, "x")}
	if st.Files != 2 || st.Bytes != 12 || !slices.Equal(seen, want) {
		t.Fatalf("stats %+v, seen %v, want %v", st, seen, want)
	}
	if !slices.Equal(dirs, []string{filepath.Join(link, "sub")}) {
		t.Fatalf("dirs = %v", dirs)
	}
	// a link to a file, a broken link and a missing root give nothing
	file := filepath.Join(target, "x")
	flink := filepath.Join(t.TempDir(), "flink")
	os.Symlink(file, flink)
	broken := filepath.Join(t.TempDir(), "broken")
	os.Symlink(filepath.Join(target, "nowhere"), broken)
	for _, r := range []string{flink, broken, filepath.Join(target, "missing")} {
		if st := Walk(context.Background(), r, Options{}); st.Files != 0 || st.Dirs != 0 {
			t.Fatalf("walked %q: %+v", r, st)
		}
	}
}

func TestWalkSkipDir(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "keep", "a"), 1)
	write(t, filepath.Join(root, "skip", "deep", "b"), 1)
	var names []string
	Walk(context.Background(), root, Options{
		SkipDir: func(_, name string, depth int) bool { return depth == 1 && name == "skip" },
		OnFile:  func(f File) { names = append(names, f.Name) },
	})
	if !slices.Equal(names, []string{"a"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestWalkMaxEntries(t *testing.T) {
	root := t.TempDir()
	for i := range 50 {
		write(t, filepath.Join(root, "f", string(rune('a'+i%26))+string(rune('a'+i/26))), 1)
	}
	st := Walk(context.Background(), root, Options{MaxEntries: 10, Concurrency: 1})
	if !st.Truncated {
		t.Fatalf("not truncated: %+v", st)
	}
	if st.Files+st.Dirs > 12 {
		t.Fatalf("walked far past the limit: %+v", st)
	}
}

func TestWalkCancel(t *testing.T) {
	root := t.TempDir()
	for i := range 20 {
		write(t, filepath.Join(root, string(rune('a'+i)), "f"), 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	st := Walk(ctx, root, Options{
		Concurrency: 1,
		OnFile: func(File) {
			if n.Add(1) == 3 {
				cancel()
			}
		},
	})
	if !st.Aborted {
		t.Fatalf("not aborted: %+v", st)
	}
	if st.Files >= 20 {
		t.Fatalf("kept walking after cancel: %+v", st)
	}
}

func TestWalkParallelIsConsistent(t *testing.T) {
	root := t.TempDir()
	var want int64
	for i := range 40 {
		for j := range 5 {
			write(t, filepath.Join(root, "d"+string(rune('a'+i%10)), "s"+string(rune('a'+i/10)), string(rune('a'+j))+string(rune('a'+i))), i+j+1)
			want += int64(i + j + 1)
		}
	}
	st := Walk(context.Background(), root, Options{Concurrency: 8})
	if st.Bytes != want || st.Files != 200 {
		t.Fatalf("stats = %+v, want %d bytes", st, want)
	}
}

func TestSize(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b"), 7)
	write(t, filepath.Join(root, "c"), 3)
	if got := Size(context.Background(), root); got != 10 {
		t.Errorf("dir size = %d", got)
	}
	if got := Size(context.Background(), filepath.Join(root, "c")); got != 3 {
		t.Errorf("file size = %d", got)
	}
	if err := os.Symlink(root, filepath.Join(t.TempDir(), "l")); err == nil {
		l := filepath.Join(t.TempDir(), "l2")
		_ = os.Symlink(root, l)
		if got := Size(context.Background(), l); got != 0 {
			t.Errorf("link size = %d", got)
		}
	}
	if got := Size(context.Background(), filepath.Join(root, "nope")); got != 0 {
		t.Errorf("missing size = %d", got)
	}
}

func TestForEach(t *testing.T) {
	var sum atomic.Int64
	ForEach(context.Background(), 100, 4, func(i int) { sum.Add(int64(i)) })
	if sum.Load() != 4950 {
		t.Fatalf("sum = %d", sum.Load())
	}
	ForEach(context.Background(), 0, 4, func(int) { t.Fatal("called") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ForEach(ctx, 10, 2, func(int) { t.Fatal("called after cancel") })
}

func TestSizeIsCapped(t *testing.T) {
	root := t.TempDir()
	for i := range 40 {
		write(t, filepath.Join(root, "d", string(rune('a'+i%26))+string(rune('a'+i/26))), 1)
	}
	// the cap is far above this tree, so the exact size comes back
	if got := Size(context.Background(), root); got != 40 {
		t.Fatalf("size = %d", got)
	}
}

func TestSameDevice(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "f"), 3)
	// everything here is on one device, so the option changes nothing
	st := Walk(context.Background(), root, Options{SameDevice: true})
	if st.Bytes != 3 || st.Dirs != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// /proc is a mount of its own on Linux: a same-device walk of / must not enter it
	rootFi, err1 := os.Lstat("/")
	procFi, err2 := os.Lstat("/proc")
	if err1 != nil || err2 != nil || extra(rootFi).Dev == extra(procFi).Dev {
		t.Skip("/proc is not a separate mount here")
	}
	top := func(same bool) (found bool) {
		Walk(context.Background(), "/", Options{
			SameDevice: same, MaxEntries: 300, Concurrency: 1,
			OnDir: func(p string, depth int) {
				if depth == 1 && p == "/proc" {
					found = true
				}
			},
		})
		return found
	}
	if top(true) {
		t.Error("same-device walk entered /proc")
	}
	if !top(false) {
		t.Error("control walk did not see /proc")
	}
}

func TestIsDataless(t *testing.T) {
	tests := []struct {
		name string
		e    Extra
		want bool
	}{
		{"flag set", Extra{Dataless: true, HasBlocks: true, Blocks: 8}, true},
		{"no blocks (fallback)", Extra{HasBlocks: true}, true},
		{"normal file", Extra{HasBlocks: true, Blocks: 8}, false},
		{"no block data and no flag", Extra{}, false},
	}
	for _, tc := range tests {
		if got := tc.e.IsDataless(); got != tc.want {
			t.Errorf("%s: IsDataless = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := (File{Size: 10, Extra: Extra{Dataless: true, HasBlocks: true, Blocks: 8}}).Allocated(); got != 0 {
		t.Errorf("a flagged placeholder allocates %d, want 0", got)
	}
}

func TestAllocated(t *testing.T) {
	tests := []struct {
		name string
		f    File
		want int64
	}{
		{"blocks win where reported", File{Size: 10_000, Extra: Extra{HasBlocks: true, Blocks: 8}}, 4096},
		{"placeholder takes nothing", File{Size: 10_000, Extra: Extra{HasBlocks: true}}, 0},
		{"no block data: logical size", File{Size: 10_000, Extra: Extra{Blocks: 8}}, 10_000},
	}
	for _, tc := range tests {
		if got := tc.f.Allocated(); got != tc.want {
			t.Errorf("%s: Allocated = %d, want %d", tc.name, got, tc.want)
		}
	}
	p := filepath.Join(t.TempDir(), "f")
	write(t, p, 123)
	fi, _ := os.Lstat(p)
	if got := Allocated(fi); got <= 0 {
		t.Errorf("Allocated(real file) = %d", got)
	}
}

// SkipDirConcurrent runs outside the walker's lock: two calls overlap. Under
// the lock the second call could never start while the first one waits.
func TestWalkSkipDirConcurrentRunsWithoutTheLock(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/x", "b/x", "c/x"} {
		write(t, filepath.Join(root, d, "f.txt"), 1)
	}
	var arrived atomic.Int32
	var overlapped atomic.Bool
	var files atomic.Int32
	Walk(context.Background(), root, Options{
		SkipDirConcurrent: func(p, name string, depth int) bool {
			if depth != 2 {
				return false
			}
			arrived.Add(1)
			deadline := time.Now().Add(3 * time.Second)
			for arrived.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if arrived.Load() >= 2 {
				overlapped.Store(true)
			}
			return false
		},
		OnFile: func(File) { files.Add(1) },
	})
	if !overlapped.Load() {
		t.Fatal("SkipDirConcurrent calls never overlapped: still run under the lock")
	}
	if files.Load() != 3 {
		t.Fatalf("files = %d, want 3", files.Load())
	}
}

func TestWalkSkipDirConcurrentSkips(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "keep", "a.txt"), 1)
	write(t, filepath.Join(root, "skip", "b.txt"), 1)
	var got []string
	Walk(context.Background(), root, Options{
		SkipDirConcurrent: func(_, name string, _ int) bool { return name == "skip" },
		OnFile:            func(f File) { got = append(got, f.Name) },
	})
	if !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("files = %v, want only a.txt", got)
	}
}

func TestWalkCountsUnreadableFolders(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list any folder")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "ok", "a.txt"), 5)
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "new.txt"), 5)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	st := Walk(context.Background(), root, Options{})
	if st.Unreadable != 1 || st.Files != 1 {
		t.Fatalf("stats = %+v, want 1 unreadable folder and 1 file seen", st)
	}
	if st := Walk(context.Background(), locked, Options{}); st.Unreadable != 1 {
		t.Fatalf("unreadable root: stats = %+v", st)
	}
	os.Chmod(locked, 0o755)
	if st := Walk(context.Background(), root, Options{}); st.Unreadable != 0 {
		t.Fatalf("readable tree: stats = %+v", st)
	}
}

// The status change time of a file and of a folder is read, and a folder's
// own lstat result reaches OnDirInfo.
func TestWalkReportsStatusChangeTimes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "d", "a.txt"), 5)
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{filepath.Join(root, "d", "a.txt"), filepath.Join(root, "d")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	var fileC, dirC, dirM int64
	Walk(context.Background(), root, Options{
		OnFile: func(f File) { fileC = f.CTime },
		OnDirInfo: func(_ string, _ int, fi fs.FileInfo) {
			dirC = CTime(fi)
			dirM, _, _ = Times(fi)
		},
	})
	recent := time.Now().Add(-time.Minute).UnixMilli()
	if fileC < recent || dirC < recent {
		t.Fatalf("ctime of file %d and folder %d, want within the last minute (%d)", fileC, dirC, recent)
	}
	if dirM > recent {
		t.Fatalf("folder mtime %d should be the old one set", dirM)
	}
}
