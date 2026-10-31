//go:build windows

package protection

import (
	"context"
	"os"
	"path/filepath"

	"dev.agus.bersihin/internal/sys"
)

// New returns a Checker that reads Windows Security through Windows
// PowerShell, without a profile and without asking for any rights.
func New() *Checker {
	root := os.Getenv("SystemRoot")
	if root == "" || !filepath.IsAbs(root) {
		root = `C:\Windows`
	}
	ps := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	enc := EncodedCommand(WindowsScript)
	return &Checker{Windows: func(ctx context.Context) (string, error) {
		res, err := sys.Exec{}.Run(ctx, ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", enc)
		return res.Stdout, err
	}}
}
