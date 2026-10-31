//go:build windows

package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"dev.agus.bersihin/internal/junk"
	"dev.agus.bersihin/internal/sys"
)

// lastUsed is the junk scan's extra "last used" signal on Windows: the
// shortcuts of Recent Items, which File Explorer dates when a file is opened.
func lastUsed(sys.Runner) func(ctx context.Context, paths []string) (map[string]int64, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return nil
	}
	return junk.RecentItemsLastUsed(filepath.Join(appData, "Microsoft", "Windows", "Recent"))
}

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
