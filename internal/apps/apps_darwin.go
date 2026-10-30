//go:build darwin

package apps

import (
	"path/filepath"

	"dev.agus.bersihin/internal/sys"
)

// New returns a Manager that reads the Mac's Applications folders with
// plutil, du and mdls. trash is Shell.TrashItem.
func New(home string, trash func(string) error) *Manager {
	return &Manager{
		Run:   sys.Exec{},
		Home:  home,
		Roots: []string{"/Applications", filepath.Join(home, "Applications")},
		Trash: trash,
	}
}
