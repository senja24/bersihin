//go:build windows

package assistant

import "syscall"

// errConnRefused is WSAECONNREFUSED, which a refused connection returns on
// Windows: Go's syscall.ECONNREFUSED is another number there and does not match it.
var errConnRefused error = syscall.Errno(10061)
