//go:build unix

package junk

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"dev.agus.bersihin/internal/walk"
)

// isLocked: the file is held by another process.
func isLocked(err error) bool { return errors.Is(err, syscall.EBUSY) }

// isNotEmpty: a folder could not be removed because something is in it.
func isNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}

// linkCount is the hard link count of the file name in h, whose lstat is
// fi, 0 when unknown.
func linkCount(_ *os.Root, _ string, fi fs.FileInfo) uint64 { return walk.Nlink(fi) }
