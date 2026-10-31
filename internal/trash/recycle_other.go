//go:build !windows

package trash

// RecycleOnly returns move as it is: the Trash of macOS takes any item of
// a local volume.
func RecycleOnly(move func(string) error) func(string) error { return move }
