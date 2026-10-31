package trash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/testenv"
	"dev.agus.bersihin/internal/walk"
)

type env struct {
	home, outside string
	moved         []string
	moveErr       map[string]error
	tr            *Trasher
}

func setup(t *testing.T) *env {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	e := &env{home: filepath.Join(base, "home", "alice"), outside: filepath.Join(base, "outside"), moveErr: map[string]error{}}
	for _, d := range []string{"Downloads/stuff", "Documents", ".ssh", "Projects"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(e.outside, 0o755)
	e.tr = &Trasher{
		Guard: func(context.Context) pathsafe.Guard {
			g := pathsafe.NewGuard(e.home, nil)
			g.SystemRoots = nil
			return g
		},
		Move: func(p string) error {
			if err := e.moveErr[p]; err != nil {
				return err
			}
			e.moved = append(e.moved, p)
			return nil
		},
	}
	return e
}

func (e *env) file(t *testing.T, rel string, size int) string {
	t.Helper()
	p := filepath.Join(e.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTrashMovesAndCounts(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a.zip", 100)
	b := e.file(t, "Downloads/stuff/b.bin", 50)
	res := e.tr.Trash(context.Background(), []string{a, b, a, a + "/"})
	if res.Removed != 2 || res.Freed != 150 || res.Failed != 0 || len(res.Skipped) != 0 {
		t.Fatalf("res = %+v", res)
	}
	if !slices.Equal(e.moved, []string{a, b}) {
		t.Fatalf("moved = %v", e.moved)
	}
}

func TestTrashNestedGoesWithFolder(t *testing.T) {
	e := setup(t)
	e.file(t, "Projects/old/a", 10)
	e.file(t, "Projects/old/sub/b", 20)
	folder := filepath.Join(e.home, "Projects", "old")
	res := e.tr.Trash(context.Background(), []string{filepath.Join(folder, "sub", "b"), folder})
	if !slices.Equal(e.moved, []string{folder}) || res.Removed != 1 || res.Freed != 30 {
		t.Fatalf("moved = %v res = %+v", e.moved, res)
	}
}

func TestTrashBlocksDangerousPaths(t *testing.T) {
	e := setup(t)
	key := e.file(t, ".ssh/id_rsa", 1)
	secret := filepath.Join(e.outside, "secret")
	os.WriteFile(secret, []byte("x"), 0o644)
	testenv.Symlink(t, e.outside, filepath.Join(e.home, "Projects", "escape"))
	paths := []string{
		e.home,
		filepath.Join(e.home, "Documents"),
		filepath.Join(e.home, "Downloads"),
		key,
		secret,
		filepath.Join(e.home, "Projects", "escape"),
		filepath.Join(e.home, "Projects", "escape", "secret"),
		e.home + "/Projects/../Documents",
		"/",
		"",
		"relative/path",
	}
	res := e.tr.Trash(context.Background(), paths)
	if len(e.moved) != 0 {
		t.Fatalf("moved dangerous paths: %v", e.moved)
	}
	if res.Removed != 0 || res.Failed != len(paths) {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "11 items blocked for safety" {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("outside file vanished")
	}
}

func TestTrashSymlinkedSpellingCollapses(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a.zip", 5)
	res := e.tr.Trash(context.Background(), []string{a, filepath.Join(e.home, "Downloads", ".", "a.zip")})
	if res.Removed != 1 || len(e.moved) != 1 {
		t.Fatalf("res = %+v moved = %v", res, e.moved)
	}
}

func TestTrashReportsLockedErrorsAndGone(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/locked", 1)
	b := e.file(t, "Downloads/broken", 1)
	c := e.file(t, "Downloads/fine", 7)
	e.moveErr[a] = errors.New("The file is in use by another process")
	e.moveErr[b] = errors.New("permission denied")
	gone := filepath.Join(e.home, "Downloads", "ghost")
	res := e.tr.Trash(context.Background(), []string{a, b, c, gone})
	if res.Removed != 1 || res.Freed != 7 || res.Failed != 2 {
		t.Fatalf("res = %+v", res)
	}
	want := []string{"1 item is in use", "1 item could not be moved to the " + BinName}
	if !slices.Equal(res.Skipped, want) {
		t.Fatalf("skipped = %v, want %v", res.Skipped, want)
	}
	// "already gone" is information, not a problem
	if !slices.Equal(res.Notes, []string{"1 item was already gone"}) {
		t.Fatalf("notes = %v", res.Notes)
	}
}

func TestTrashGonePathIsANoteNotAFailure(t *testing.T) {
	e := setup(t)
	gone := filepath.Join(e.home, "Downloads", "ghost")
	res := e.tr.Trash(context.Background(), []string{gone})
	if res.Failed != 0 || len(res.Skipped) != 0 || !slices.Equal(res.Notes, []string{"1 item was already gone"}) {
		t.Fatalf("res = %+v", res)
	}
	if res.Cancelled {
		t.Error("not cancelled")
	}
}

func TestTrashNothing(t *testing.T) {
	e := setup(t)
	res := e.tr.Trash(context.Background(), nil)
	if res.Skipped == nil || res.Notes == nil || res.Removed != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestCommas(t *testing.T) {
	for n, want := range map[int]string{0: "0", 12: "12", 1234: "1,234", 1234567: "1,234,567", 100: "100", 1000: "1,000"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestTrashRechecksRightBeforeMoving(t *testing.T) {
	e := setup(t)
	victim := filepath.Join(e.outside, "precious")
	os.WriteFile(victim, []byte("x"), 0o644)
	a := e.file(t, "Downloads/a.zip", 10)
	b := e.file(t, "Downloads/b.zip", 10)
	// after the first path was judged, a.zip is replaced by a link to a file outside home
	first := true
	inner := e.tr.Move
	e.tr.Move = func(p string) error {
		if first {
			first = false
			os.Remove(b)
			testenv.Symlink(t, victim, b)
		}
		return inner(p)
	}
	res := e.tr.Trash(context.Background(), []string{a, b})
	if slices.Contains(e.moved, b) {
		t.Fatalf("moved a path that turned into a link: %v", e.moved)
	}
	if res.Removed != 1 || res.Failed != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("outside file vanished")
	}
}

func TestTrashCancelledCountsTheRest(t *testing.T) {
	e := setup(t)
	var paths []string
	for _, n := range []string{"a", "b", "c", "d"} {
		paths = append(paths, e.file(t, "Downloads/"+n, 1))
	}
	ctx, cancel := context.WithCancel(context.Background())
	inner := e.tr.Move
	e.tr.Move = func(p string) error { cancel(); return inner(p) }
	res := e.tr.Trash(ctx, paths)
	if res.Removed != 1 || res.Failed != 3 {
		t.Fatalf("res = %+v", res)
	}
	if !slices.Contains(res.Skipped, "Stopped early: 3 items were not moved") {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	if !res.Cancelled {
		t.Fatalf("Cancelled not set: %+v", res)
	}
}

func TestTrashCancelledBeforeMovingMovesNothing(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/big/a", 1)
	b := e.file(t, "Downloads/b", 1)
	ctx, cancel := context.WithCancel(context.Background())
	// the guard is asked once per call; cancel as soon as the paths are judged
	inner := e.tr.Guard
	e.tr.Guard = func(c context.Context) pathsafe.Guard { defer cancel(); return inner(c) }
	res := e.tr.Trash(ctx, []string{filepath.Dir(a), b})
	if len(e.moved) != 0 {
		t.Fatalf("moved after cancel: %v", e.moved)
	}
	if res.Removed != 0 || res.Failed != 2 || !res.Cancelled {
		t.Fatalf("res = %+v", res)
	}
}

func TestTrashFinishedIsNotCancelled(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a", 1)
	res := e.tr.Trash(context.Background(), []string{a})
	if res.Cancelled || res.Skipped == nil || res.Notes == nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestTrashCheckedMovesAnUnchangedFile(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a.bin", 10)
	fi, _ := walk.Lstat(a)
	res := e.tr.TrashChecked(context.Background(), []string{a}, map[string]Identity{a: IdentityOf(fi)})
	if res.Removed != 1 || res.Failed != 0 || !slices.Equal(e.moved, []string{a}) {
		t.Fatalf("res = %+v moved = %v", res, e.moved)
	}
}

// H1: between the caller's check and the move the file was replaced by
// another one with the same size and mtime.
func TestTrashCheckedRefusesAReplacedFile(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a.bin", 10)
	fi, _ := walk.Lstat(a)
	want := IdentityOf(fi)
	other := e.file(t, "Downloads/other.bin", 10)
	os.Chtimes(other, fi.ModTime(), fi.ModTime())
	if err := os.Rename(other, a); err != nil { // a new inode under the old name
		t.Fatal(err)
	}
	res := e.tr.TrashChecked(context.Background(), []string{a}, map[string]Identity{a: want})
	if len(e.moved) != 0 || res.Removed != 0 || res.Failed != 1 {
		t.Fatalf("res = %+v moved = %v", res, e.moved)
	}
	if !slices.Contains(res.Skipped, "1 file changed before it was moved") {
		t.Fatalf("skipped = %v", res.Skipped)
	}
}

func TestTrashCheckedRefusesChangedSizeMtimeLinkAndUnexpected(t *testing.T) {
	e := setup(t)
	grown := e.file(t, "Downloads/grown", 10)
	touched := e.file(t, "Downloads/touched", 10)
	linked := e.file(t, "Downloads/linked", 10)
	unexpected := e.file(t, "Downloads/unexpected", 10)
	expect := map[string]Identity{}
	for _, p := range []string{grown, touched, linked} {
		fi, _ := walk.Lstat(p)
		expect[p] = IdentityOf(fi)
	}
	os.WriteFile(grown, make([]byte, 11), 0o644)
	later := time.Now().Add(time.Hour)
	os.Chtimes(touched, later, later)
	target := e.file(t, "Documents/real", 10)
	os.Remove(linked)
	testenv.Symlink(t, target, linked)
	res := e.tr.TrashChecked(context.Background(), []string{grown, touched, linked, unexpected}, expect)
	if len(e.moved) != 0 || res.Failed != 4 {
		t.Fatalf("res = %+v moved = %v", res, e.moved)
	}
}

func TestTrashWithoutExpectationsIsUnchanged(t *testing.T) {
	e := setup(t)
	a := e.file(t, "Downloads/a.bin", 10)
	if res := e.tr.Trash(context.Background(), []string{a}); res.Removed != 1 {
		t.Fatalf("res = %+v", res)
	}
}

// A folder that holds a mount point (another device) anywhere inside is not
// moved, and the size walk does not enter the mount; a plain folder still goes.
func TestTrashRefusesFolderHoldingAMountPoint(t *testing.T) {
	e := setup(t)
	e.file(t, "Downloads/plain/deep/a.bin", 10)
	e.file(t, "Downloads/withmount/a.bin", 10)
	e.file(t, "Downloads/withmount/deep/mnt/b.bin", 10)
	e.file(t, "Downloads/unknown/deep/c.bin", 10)
	mnt := filepath.Join(e.home, "Downloads", "withmount", "deep", "mnt")
	unknown := filepath.Join(e.home, "Downloads", "unknown", "deep")
	was := devOf
	devOf = func(p string) uint64 {
		switch p {
		case mnt:
			return was(p) + 1 // another device
		case unknown:
			return 0 // device not known
		}
		return was(p)
	}
	t.Cleanup(func() { devOf = was })

	plain := filepath.Join(e.home, "Downloads", "plain")
	paths := []string{plain, filepath.Join(e.home, "Downloads", "withmount"), filepath.Join(e.home, "Downloads", "unknown")}
	res := e.tr.Trash(context.Background(), paths)
	if !slices.Equal(e.moved, []string{plain}) {
		t.Fatalf("moved = %v, want only %s", e.moved, plain)
	}
	if res.Removed != 1 || res.Failed != 2 || res.Freed != 10 {
		t.Fatalf("res = %+v, want 1 removed, 2 failed, only the plain folder counted", res)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("skipped = %v, want one line about folders holding another volume", res.Skipped)
	}
	if want := "2 folders hold another volume and were left alone"; res.Skipped[0] != want {
		t.Fatalf("skipped = %q, want %q", res.Skipped[0], want)
	}
}

// One folder with a mount reads "1 folder holds ... and was left alone".
func TestTrashMountedFolderMessageIsSingular(t *testing.T) {
	e := setup(t)
	e.file(t, "Downloads/withmount/deep/mnt/b.bin", 10)
	mnt := filepath.Join(e.home, "Downloads", "withmount", "deep", "mnt")
	was := devOf
	devOf = func(p string) uint64 {
		if p == mnt {
			return was(p) + 1
		}
		return was(p)
	}
	t.Cleanup(func() { devOf = was })
	res := e.tr.Trash(context.Background(), []string{filepath.Join(e.home, "Downloads", "withmount")})
	if want := "1 folder holds another volume and was left alone"; len(res.Skipped) != 1 || res.Skipped[0] != want {
		t.Fatalf("skipped = %q, want %q", res.Skipped, want)
	}
}

// The size walk of a folder stops at a mount point: nothing below it is counted.
func TestSizeOfDoesNotCrossMounts(t *testing.T) {
	e := setup(t)
	e.file(t, "Downloads/f/a.bin", 10)
	e.file(t, "Downloads/f/mnt/b.bin", 1000)
	mnt := filepath.Join(e.home, "Downloads", "f", "mnt")
	was := devOf
	devOf = func(p string) uint64 {
		if p == mnt {
			return was(p) + 1
		}
		return was(p)
	}
	t.Cleanup(func() { devOf = was })
	size, crossed := sizeOf(context.Background(), filepath.Join(e.home, "Downloads", "f"))
	if !crossed || size != 10 {
		t.Fatalf("size = %d, crossed = %v, want 10 and true", size, crossed)
	}
}

// A folder is moved only while it is still the folder the caller saw.
func TestTrashCheckedFolderIdentity(t *testing.T) {
	e := setup(t)
	dir := filepath.Join(e.home, "Downloads", "d")
	e.file(t, "Downloads/d/a.bin", 10)
	fi, err := walk.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := IdentityOf(fi)
	want.Dir = true
	res := e.tr.TrashChecked(context.Background(), []string{dir}, map[string]Identity{dir: want})
	if res.Removed != 1 || res.Failed != 0 {
		t.Fatalf("same folder: res = %+v", res)
	}

	e2 := setup(t)
	dir2 := filepath.Join(e2.home, "Downloads", "d")
	e2.file(t, "Downloads/d/a.bin", 10)
	fi2, _ := walk.Lstat(dir2)
	id := IdentityOf(fi2)
	id.Dir = true
	if err := os.Rename(dir2, dir2+"-old"); err != nil {
		t.Fatal(err)
	}
	e2.file(t, "Downloads/d/a.bin", 10) // another folder under the same name
	res = e2.tr.TrashChecked(context.Background(), []string{dir2}, map[string]Identity{dir2: id})
	if res.Removed != 0 || res.Failed != 1 || len(e2.moved) != 0 {
		t.Fatalf("swapped folder: res = %+v moved = %v", res, e2.moved)
	}
	// a file where the folder was is not the folder either
	file := e2.file(t, "Downloads/f", 10)
	res = e2.tr.TrashChecked(context.Background(), []string{file}, map[string]Identity{file: {Dir: true}})
	if res.Removed != 0 || res.Failed != 1 {
		t.Fatalf("file for folder: res = %+v", res)
	}
}
