//go:build unix

package walk

import (
	"io/fs"
	"os"
)

// On Unix the lstat result carries the identity: nothing more to read.

func lstat(p string) (fs.FileInfo, error) { return os.Lstat(p) }

func dirInfo(_ string, d fs.DirEntry) (fs.FileInfo, error) { return d.Info() }

func fileInfo(_ string, d fs.DirEntry, _ bool) (fs.FileInfo, error) { return d.Info() }
