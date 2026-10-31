//go:build !windows

package sys

import "os/exec"

func hideWindow(*exec.Cmd) {}
