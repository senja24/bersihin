//go:build !darwin

package startup

// New returns a Manager that lists nothing: launchd exists only on macOS.
func New(home string) *Manager { return &Manager{} }
