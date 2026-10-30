//go:build !darwin

package disks

// New returns a Lister that knows only the system volume: there is no mount
// listing to read off macOS.
func New(home string) *Lister {
	return &Lister{Home: home, Label: "/"}
}
