//go:build darwin

package disks

import (
	"time"

	"dev.agus.bersihin/internal/sys"
)

// New returns a Lister that asks the Mac: /sbin/mount and /usr/bin/hdiutil.
func New(home string) *Lister {
	return &Lister{Run: sys.Exec{Timeout: 5 * time.Second}, Home: home, Label: systemLabel}
}
