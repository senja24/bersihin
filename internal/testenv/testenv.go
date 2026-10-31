// Package testenv holds the skips the tests share, each with its reason, so
// that the whole Go suite runs (and gates CI) on Windows as on macOS. A test
// skipped on Windows tests the Mac's rules or relies on what only a Unix file
// system does; the Windows rules have tests of their own (the *Windows* tests).
package testenv

import (
	"os"
	"runtime"
	"testing"
)

// Windows is true when the tests run on Windows.
const Windows = runtime.GOOS == "windows"

// MacPaths skips a test of the Mac's rules written with POSIX paths
// (/Users/alice/Library): on Windows these are not absolute paths, and the
// Mac guard and categories are not used there.
func MacPaths(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("tests the macOS rules on POSIX paths; Windows uses its own rules, tested by the Windows tests")
	}
}

// Symlink makes newname a symbolic link to oldname and returns os.Symlink's
// error, or skips the test on Windows: creating one there needs Developer
// Mode or administrator rights, so whether the test would run would depend on
// the machine, and Bersihin refuses every link and junction outright on
// Windows, which the Windows tests check.
func Symlink(t testing.TB, oldname, newname string) error {
	t.Helper()
	NoSymlinks(t)
	return os.Symlink(oldname, newname)
}

// NoSymlinks skips a test that needs symbolic links on Windows, for tests that
// make them in a helper of their own.
func NoSymlinks(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("relies on symbolic links, whose handling is the Mac's; on Windows links and junctions are refused outright (see the Windows tests)")
	}
}

// Permissions skips a test that makes a file or folder unreadable with
// chmod: Windows keeps only the read-only bit, so access cannot be denied
// that way.
func Permissions(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("denies access with Unix permission bits, which Windows does not have")
	}
}

// CaseSensitive skips a test that needs names differing only in case to be
// different files, as on a case-sensitive Mac volume; NTFS folds case.
func CaseSensitive(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("needs a case-sensitive file system; NTFS folds case")
	}
}

// StatusChange skips a test that relies on the Unix status change time
// (ctime) moving when a file's metadata changes.
func StatusChange(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("relies on the Unix status change time; Windows' change time is read through a handle and behaves differently")
	}
}

// MacLayout skips a test that builds a Mac home folder (Library, Movies,
// .Trash) and expects the Mac's categories and rules for it: on Windows the
// same code picks the Windows categories and guard, which have tests of
// their own on a Windows profile.
func MacLayout(t testing.TB) {
	t.Helper()
	if Windows {
		t.Skip("expects the macOS categories and rules for a Mac home folder; Windows uses its own, tested on a Windows profile by the Windows tests")
	}
}
