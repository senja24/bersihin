// Package sys runs external programs: argument lists only, never a shell.
package sys

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// Result is what a finished program printed.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs a program with an argument list. A non-zero exit status is an
// error, but the Result still holds what was printed.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// RunFunc adapts a function to a Runner (handy in tests).
type RunFunc func(ctx context.Context, name string, args ...string) (Result, error)

// Run calls f.
func (f RunFunc) Run(ctx context.Context, name string, args ...string) (Result, error) {
	return f(ctx, name, args...)
}

// Exec runs real programs with os/exec.
type Exec struct {
	// Timeout bounds every run. Zero means 15 seconds, unless the context
	// already has a deadline of its own.
	Timeout time.Duration
}

// Run starts name with args (no shell) and waits for it.
func (e Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	if _, has := ctx.Deadline(); e.Timeout > 0 || !has {
		d := e.Timeout
		if d <= 0 {
			d = 15 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	hideWindow(cmd)
	// a child that ignores the kill and holds the pipes open must not hang the call
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
	}
	return res, err
}
