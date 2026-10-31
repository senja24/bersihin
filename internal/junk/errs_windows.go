//go:build windows

package junk

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"dev.agus.bersihin/internal/walk"
)

const (
	errorSharingViolation = syscall.Errno(32)
	errorLockViolation    = syscall.Errno(33)
	errorDirNotEmpty      = syscall.Errno(145)
)

// isLocked: the file is open in another process without delete sharing.
func isLocked(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
}

// isNotEmpty: a folder could not be removed because something is in it.
func isNotEmpty(err error) bool { return errors.Is(err, errorDirNotEmpty) }

// linkCount is the hard link count of the file name in h, read through a
// handle (a handle's lstat carries none on Windows), 0 when unknown: a
// file that cannot be opened counts as shared, and frees nothing.
func linkCount(h *os.Root, name string, _ fs.FileInfo) uint64 {
	f, err := h.Open(name)
	if err != nil {
		return 0
	}
	defer f.Close()
	return walk.HandleNlink(f)
}
