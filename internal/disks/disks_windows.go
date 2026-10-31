//go:build windows

package disks

import (
	"context"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives     = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW        = kernel32.NewProc("GetDriveTypeW")
	procGetVolumeInformation = kernel32.NewProc("GetVolumeInformationW")
	procGetDiskFreeSpaceExW  = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// New returns a Lister of the fixed drives (GetLogicalDrives and
// GetDriveTypeW); the drive of home is the system volume.
func New(home string) *Lister {
	root := SystemRoot(home)
	label := "Local Disk"
	if root != "" {
		label = volumeLabel(root)
		if label == "" {
			label = "Local Disk"
		}
		label += " (" + root[:2] + ")"
	}
	return &Lister{Home: home, Label: label, SystemMount: root, Drives: func(context.Context) ([]Volume, bool) {
		mask, _, _ := procGetLogicalDrives.Call()
		if mask == 0 {
			return nil, false
		}
		return LetterVolumes(uint32(mask), home, driveType, volumeLabel), true
	}}
}

func driveType(root string) uint32 {
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0
	}
	r, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r)
}

func volumeLabel(root string) string {
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	var name [261]uint16
	r, _, _ := procGetVolumeInformation.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)), 0, 0, 0, 0, 0)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(name[:])
}

// statfs is the size of the volume holding p and the space free for this
// user (quotas included), from GetDiskFreeSpaceExW.
func statfs(p string) (total, free int64, err error) {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return 0, 0, err
	}
	var avail, size, totalFree uint64
	r, _, e := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, 0, e
	}
	return int64(size), int64(avail), nil
}
