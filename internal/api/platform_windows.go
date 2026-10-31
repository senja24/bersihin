//go:build windows

package api

import (
	"fmt"
	"syscall"
	"unsafe"
)

// fileManager is the app RevealPath shows files in.
const fileManager = "File Explorer"

// unsupportedScreens lists the screens the page hides on Windows: none now
// that Applications, Speed and Protection read Windows' own places.
func unsupportedScreens() []string { return []string{} }

var procRtlGetVersion = syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion")

// osVersionInfo is RTL_OSVERSIONINFOW.
type osVersionInfo struct {
	size                     uint32
	major, minor, build, pid uint32
	csd                      [128]uint16
}

// osVersion is "10.0.26100": RtlGetVersion reports the real version, where
// GetVersionEx reports what the manifest asks for.
func osVersion() string {
	v := osVersionInfo{size: uint32(unsafe.Sizeof(osVersionInfo{}))}
	if r, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v))); r != 0 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.build)
}
