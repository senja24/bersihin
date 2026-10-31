//go:build windows

package testenv

import (
	"os"
	"syscall"
	"time"
)

const fsctlSetSparse = 0x000900C4

// setCreation moves the creation time of p back to mtime when mtime is older,
// as macOS moves the birth time back when a file's modification time is set
// before it.
func setCreation(p string, mtime time.Time) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	ft := syscall.NsecToFiletime(mtime.UnixNano())
	if !ok || ft.Nanoseconds() >= d.CreationTime.Nanoseconds() {
		return nil
	}
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(name, syscall.FILE_WRITE_ATTRIBUTES,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	return syscall.SetFileTime(h, &ft, nil, nil)
}

func sparse(f *os.File) error {
	var n uint32
	return syscall.DeviceIoControl(syscall.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &n, nil)
}
