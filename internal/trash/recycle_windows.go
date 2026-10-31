//go:build windows

package trash

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

const driveFixed = 3

var procGetDriveTypeW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDriveTypeW")

// RecycleOnly wraps move (MyGo's TrashItem) so that only items on a fixed
// local drive reach it: removable drives and network shares have no Recycle
// Bin, and Windows would delete their items for good. MyGo refuses such a
// delete too; this is the check before it.
func RecycleOnly(move func(string) error) func(string) error {
	return func(p string) error {
		if err := recyclable(p); err != nil {
			return err
		}
		return move(p)
	}
}

func recyclable(p string) error {
	vol := filepath.VolumeName(p)
	if len(vol) != 2 || vol[1] != ':' {
		return fmt.Errorf("%s is not on a local drive with a Recycle Bin", p)
	}
	root, err := syscall.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return err
	}
	if t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(root))); t != driveFixed {
		return fmt.Errorf("%s is not on a fixed drive: it has no Recycle Bin", p)
	}
	return nil
}
