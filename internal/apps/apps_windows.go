//go:build windows

package apps

import (
	"os"

	"golang.org/x/sys/windows"

	"dev.agus.bersihin/internal/winreg"
)

// New returns a Manager that lists the programs of the Uninstall keys and
// starts their own uninstallers. trash is unused: Windows programs are not
// moved to the Recycle Bin.
func New(home string, trash func(string) error) *Manager {
	return &Manager{Home: home, Registry: winreg.System{}, Launch: launch, Getenv: os.Getenv}
}

// launch starts exe with args through ShellExecute, as Settings does. An
// uninstaller that needs administrator rights makes Windows show its UAC
// prompt, where the user may still say no.
func launch(exe, args string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}
