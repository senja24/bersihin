//go:build !darwin && !windows

package protection

// New returns a Checker that reports "unknown": the tools exist only on macOS.
func New() *Checker { return &Checker{} }
