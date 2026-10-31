package junk

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dev.agus.bersihin/internal/testenv"
	"dev.agus.bersihin/internal/walk"
)

// A status change time cannot be set back, so these tests keep the times of
// the files old (os.Chtimes), note the latest status change time of the
// tree as t0, change one thing after a pause, and read the tree with a clock
// that puts the 90-day cutoff between t0 and that change. Only the change
// can then keep the entry.

const ctimePause = 150 * time.Millisecond

// ctimeMs is the latest status change time of the paths, epoch ms.
func ctimeMs(t *testing.T, paths ...string) int64 {
	t.Helper()
	var latest int64
	for _, p := range paths {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		latest = max(latest, walk.CTime(fi))
	}
	return latest
}

// clockFor makes the cutoff of old downloads fall 50 ms after t0.
func clockFor(f *fixture, t0 int64) {
	f.sc.ignoreCtime = false
	f.sc.Now = func() time.Time { return time.UnixMilli(t0 + 50).Add(oldDownload) }
}

// treeTime is the one time every file and folder of agedTree has.
var treeTime = time.Now().Add(-old).Truncate(time.Millisecond)

func setOld(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.Chtimes(p, treeTime, treeTime); err != nil {
			t.Fatal(err)
		}
	}
}

// agedTree makes proj/sub/a.bin and proj/b.bin old in every time that can be
// set, all to the same time, and returns the latest ctime of all of it.
func agedTree(t *testing.T, f *fixture) (proj string, t0 int64) {
	t.Helper()
	proj = f.p("Downloads", "proj")
	a := put(t, filepath.Join(proj, "sub", "a.bin"), 100, old)
	b := put(t, filepath.Join(proj, "b.bin"), 100, old)
	setOld(t, a, b, filepath.Join(proj, "sub"), proj, f.p("Downloads"))
	return proj, ctimeMs(t, proj, filepath.Join(proj, "sub"), a, b)
}

func offered(f *fixture, p string) bool {
	return slices.Contains(f.sc.findOldDownloads(context.Background()).paths, p)
}

// Finding 1: reading a file inside a folder does not update its access time
// on APFS, but an app that records an open (the last-used extended attribute)
// or a change of attributes moves the status change time.
func TestFileInsideFolderStatusChangeKeepsTheFolder(t *testing.T) {
	testenv.StatusChange(t)
	f := newFixture(t)
	proj, t0 := agedTree(t, f)
	clockFor(f, t0)
	if !offered(f, proj) {
		t.Fatal("control: a folder untouched since t0 should be offered")
	}
	time.Sleep(ctimePause)
	if err := os.Chmod(filepath.Join(proj, "b.bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if offered(f, proj) {
		t.Fatal("folder offered although a file inside changed its status after the cutoff")
	}
}

// Finding 3: a subfolder that was changed (here only its status, as when a
// file is moved into it) keeps the folder.
func TestSubfolderStatusChangeKeepsTheFolder(t *testing.T) {
	f := newFixture(t)
	proj, t0 := agedTree(t, f)
	clockFor(f, t0)
	time.Sleep(ctimePause)
	if err := os.Chmod(filepath.Join(proj, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if offered(f, proj) {
		t.Fatal("folder offered although a subfolder changed after the cutoff")
	}
}

// A subfolder changed after the cutoff keeps the folder by its modification
// time too, which is set here (ctime is ignored).
func TestSubfolderModificationKeepsTheFolder(t *testing.T) {
	f := newFixture(t)
	proj, _ := agedTree(t, f)
	at(t, filepath.Join(proj, "sub"), time.Hour, time.Hour)
	if offered(f, proj) {
		t.Fatal("folder offered although a subfolder was modified an hour ago")
	}
}

// Finding 8: a top-level file counts by its status change time as well.
func TestTopLevelFileStatusChangeKeepsIt(t *testing.T) {
	testenv.StatusChange(t)
	f := newFixture(t)
	file := put(t, f.p("Downloads", "doc.pdf"), 100, old)
	at(t, file, old, old)
	clockFor(f, ctimeMs(t, file))
	if !offered(f, file) {
		t.Fatal("control: a file untouched since t0 should be offered")
	}
	time.Sleep(ctimePause)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if offered(f, file) {
		t.Fatal("file offered although its status changed after the cutoff")
	}
}

// With a real clock the status change time of anything just written is
// recent: a folder of old files is kept, until the clock has moved on.
func TestFreshStatusChangeKeepsTheFolderUntilItIsOld(t *testing.T) {
	f := newFixture(t)
	proj, _ := agedTree(t, f)
	f.sc.ignoreCtime = false
	if offered(f, proj) {
		t.Fatal("folder offered although every status change is just now")
	}
	f.sc.Now = func() time.Time { return time.Now().Add(oldDownload + time.Hour) }
	if !offered(f, proj) {
		t.Fatal("folder kept although every time, status change included, is past the cutoff")
	}
}

// Finding 2: a subfolder that cannot be listed may hold anything.
func TestFolderWithUnreadableSubfolderIsKept(t *testing.T) {
	testenv.Permissions(t)
	if os.Geteuid() == 0 {
		t.Skip("root can list any folder")
	}
	f := newFixture(t)
	proj := f.p("Downloads", "proj")
	put(t, filepath.Join(proj, "old.bin"), 10, old)
	fresh := put(t, filepath.Join(proj, "locked", "new.bin"), 10, time.Hour)
	locked := filepath.Join(proj, "locked")
	at(t, fresh, old, old) // so that only the unreadable folder can be the reason
	at(t, locked, old, old)
	at(t, filepath.Join(proj, "old.bin"), old, old)
	at(t, proj, old, old)
	if !offered(f, proj) {
		t.Fatal("control: the readable folder should be offered")
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if offered(f, proj) {
		t.Fatal("folder with an unreadable subfolder offered")
	}
	// and the clean, which judges again, keeps it too
	os.Chmod(locked, 0o755)
	scanned(t, f)
	os.Chmod(locked, 0)
	clean(t, f.sc)
	if len(f.trashed) != 0 {
		t.Fatalf("trashed = %v", f.trashed)
	}
}

// Finding 3: a file moved into a subfolder after the scan keeps its own old
// times, and the folder's times are old too here, but the total changed.
func TestCleanKeepsFolderWhoseTotalChangedSinceTheScan(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, f *fixture, proj string)
	}{
		{"file added", func(t *testing.T, f *fixture, proj string) {
			setOld(t, put(t, filepath.Join(proj, "sub", "moved.bin"), 100, old))
		}},
		{"file replaced by a bigger one", func(t *testing.T, f *fixture, proj string) {
			setOld(t, put(t, filepath.Join(proj, "b.bin"), 300, old))
		}},
		{"file removed", func(t *testing.T, f *fixture, proj string) {
			os.Remove(filepath.Join(proj, "b.bin"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			proj, _ := agedTree(t, f)
			scanned(t, f)
			tc.change(t, f, proj)
			// the changed folders look as old as they did
			setOld(t, filepath.Join(proj, "sub"), proj)
			clean(t, f.sc)
			if len(f.trashed) != 0 || !exists(proj) {
				t.Fatalf("trashed = %v, want the folder kept", f.trashed)
			}
		})
	}
}

// The same, with the status change time read: the scan runs on a clock that
// is past every ctime, a file moves in afterwards, and the folder stays.
func TestCleanKeepsFolderAfterAFileMovedIn(t *testing.T) {
	f := newFixture(t)
	proj, _ := agedTree(t, f)
	f.sc.ignoreCtime = false
	f.sc.Now = func() time.Time { return time.Now().Add(oldDownload + time.Hour) }
	elsewhere := put(t, f.p("Documents", "x.bin"), 100, old)
	setOld(t, elsewhere)
	scanned(t, f)
	if err := os.Rename(elsewhere, filepath.Join(proj, "sub", "x.bin")); err != nil {
		t.Fatal(err)
	}
	clean(t, f.sc)
	if len(f.trashed) != 0 || !exists(proj) {
		t.Fatalf("trashed = %v, want the folder kept", f.trashed)
	}
}

// A file deleted and replaced by one of the same size and times, whatever
// inode it is given (a filesystem may hand the freed number out again), has a
// different status change time. Here the scan's reading of it is changed
// directly, as the replacement would change it, with ctime read.
func TestCleanKeepsFileWhoseStatusChangeTimeDiffers(t *testing.T) {
	f := newFixture(t)
	file := put(t, f.p("Downloads", "a.dmg"), 50, old)
	setOld(t, file, f.p("Downloads"))
	f.sc.ignoreCtime = false
	f.sc.Now = func() time.Time { return time.Now().Add(oldDownload + time.Hour) }
	scanned(t, f)
	for p, s := range f.sc.Downloads {
		s.Ino = 0 // as if the inode number were the same
		s.CTime -= 5
		f.sc.Downloads[p] = s
	}
	clean(t, f.sc)
	if len(f.trashed) != 0 {
		t.Fatalf("trashed = %v, want the file kept", f.trashed)
	}
}
