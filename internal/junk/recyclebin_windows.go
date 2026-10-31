//go:build windows

package junk

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32               = windows.NewLazySystemDLL("shell32.dll")
	procSHQueryRecycleBin = shell32.NewProc("SHQueryRecycleBinW")
	procSHEmptyRecycleBin = shell32.NewProc("SHEmptyRecycleBinW")
)

// shQueryRBInfo is SHQUERYRBINFO. shellapi.h packs it to 1 byte on 32-bit
// Windows only; Go lays it out the same way (an int64 field is aligned to 4
// bytes on 386 and to 8 on amd64 and arm64).
type shQueryRBInfo struct {
	cbSize   uint32
	size     int64
	numItems int64
}

const (
	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
)

type shellBin struct{}

var systemBin RecycleBin = shellBin{}

// Query sums the Recycle Bins of every drive (a nil root path).
func (shellBin) Query() (BinSize, error) {
	info := shQueryRBInfo{cbSize: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	if hr, _, _ := procSHQueryRecycleBin.Call(0, uintptr(unsafe.Pointer(&info))); hr != 0 {
		return BinSize{}, fmt.Errorf("SHQueryRecycleBinW: 0x%08x", uint32(hr))
	}
	return BinSize{Size: info.size, Items: info.numItems}, nil
}

// Empty empties the Recycle Bins of every drive. The user confirmed in the
// app, so the shell's own dialog, progress and sound are left out.
func (shellBin) Empty() error {
	if hr, _, _ := procSHEmptyRecycleBin.Call(0, 0, sherbNoConfirmation|sherbNoProgressUI|sherbNoSound); hr != 0 {
		return fmt.Errorf("SHEmptyRecycleBinW: 0x%08x", uint32(hr))
	}
	return nil
}
