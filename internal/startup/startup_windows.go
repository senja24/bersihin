//go:build windows

package startup

import (
	"context"
	"os"
	"path/filepath"

	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/winreg"
)

// New returns a Manager for this user's startup apps: the Run keys, the
// Startup folders of %APPDATA% and %ProgramData%, and the logon tasks.
func New(home string) *Manager {
	folder := func(env string, parts ...string) string {
		base := os.Getenv(env)
		if base == "" || !filepath.IsAbs(base) {
			return ""
		}
		return filepath.Join(append([]string{base}, parts...)...)
	}
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" || !filepath.IsAbs(sysRoot) {
		sysRoot = `C:\Windows`
	}
	schtasks := filepath.Join(sysRoot, "System32", "schtasks.exe")
	return &Manager{
		Registry: winreg.System{},
		Win: WinSources{
			UserFolder:   folder("APPDATA", "Microsoft", "Windows", "Start Menu", "Programs", "Startup"),
			CommonFolder: folder("ProgramData", "Microsoft", "Windows", "Start Menu", "Programs", "StartUp"),
			Tasks: func(ctx context.Context) (string, error) {
				res, err := sys.Exec{}.Run(ctx, schtasks, "/query", "/xml", "ONE")
				return res.Stdout, err
			},
		},
	}
}
