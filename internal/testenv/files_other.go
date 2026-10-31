//go:build !windows

package testenv

import (
	"os"
	"time"
)

// the file system moves the birth time back itself (macOS), or has none
func setCreation(string, time.Time) error { return nil }

// a file written past its end is sparse already
func sparse(*os.File) error { return nil }
