//go:build !darwin && !windows

package apps

// New returns a Manager that lists nothing: there are no app bundles to
// read off macOS.
func New(home string, trash func(string) error) *Manager {
	return &Manager{Home: home, Trash: trash}
}
