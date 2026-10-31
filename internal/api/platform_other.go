//go:build !windows

package api

import (
	"context"

	"dev.agus.bersihin/internal/junk"
	"dev.agus.bersihin/internal/sys"
)

// lastUsed is the junk scan's extra "last used" signal: Spotlight's date on a Mac.
func lastUsed(run sys.Runner) func(ctx context.Context, paths []string) (map[string]int64, error) {
	return junk.MdlsLastUsed(run)
}

// fileManager is the app RevealPath shows files in.
const fileManager = "Finder"

func osVersion() string { return "" } // sw_vers, read in systemInfo on a Mac

// unsupportedScreens lists the screens the page hides on this platform.
func unsupportedScreens() []string { return []string{} }
