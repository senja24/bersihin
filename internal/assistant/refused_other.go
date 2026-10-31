//go:build !windows

package assistant

import "syscall"

// errConnRefused is what a refused connection returns; see refused_windows.go.
var errConnRefused error = syscall.ECONNREFUSED
