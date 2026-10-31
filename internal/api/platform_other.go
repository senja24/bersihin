//go:build !windows

package api

// fileManager is the app RevealPath shows files in.
const fileManager = "Finder"

func osVersion() string { return "" } // sw_vers, read in systemInfo on a Mac

// unsupportedScreens lists the screens the page hides on this platform.
func unsupportedScreens() []string { return []string{} }
