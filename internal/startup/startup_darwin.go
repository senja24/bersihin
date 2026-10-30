//go:build darwin

package startup

import (
	"os"
	"path/filepath"

	"dev.agus.bersihin/internal/sys"
)

// New returns a Manager for this Mac's login agents: ~/Library/LaunchAgents
// and /Library/LaunchAgents (the latter may ask for the password to change).
func New(home string) *Manager {
	return &Manager{
		Run: sys.Exec{},
		UID: os.Getuid(),
		Sources: []Source{
			{Dir: filepath.Join(home, "Library", "LaunchAgents"), Prefix: "user", Location: "LaunchAgents"},
			{Dir: "/Library/LaunchAgents", Prefix: "system", Location: "LaunchAgents (all users)", Elevated: true},
		},
	}
}
