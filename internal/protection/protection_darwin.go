//go:build darwin

package protection

import "dev.agus.bersihin/internal/sys"

// New returns a Checker that asks the Mac: spctl, socketfilterfw, csrutil and plutil.
func New() *Checker { return &Checker{Run: sys.Exec{}} }
