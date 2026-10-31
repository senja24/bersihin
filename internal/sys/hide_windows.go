//go:build windows

package sys

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: console programs such as schtasks and
// PowerShell run without flashing a console window over the app.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
