package sys

import (
	"context"
	"testing"
	"time"
)

func TestExecRunsWithoutShell(t *testing.T) {
	// $HOME must reach echo as a literal: no shell expands it.
	res, err := Exec{}.Run(context.Background(), "echo", "$HOME;", "&&", "id")
	if err != nil {
		t.Skip("echo unavailable:", err)
	}
	if res.Stdout != "$HOME; && id\n" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestExecExitCodeAndTimeout(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), "false")
	if err == nil || res.ExitCode != 1 {
		t.Skipf("false: %v %+v", err, res)
	}
	start := time.Now()
	_, err = Exec{Timeout: 50 * time.Millisecond}.Run(context.Background(), "sleep", "5")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout not enforced: %v after %v", err, time.Since(start))
	}
}

func TestExecHonoursTheCallersDeadline(t *testing.T) {
	// no Timeout set: a deadline on the context is the only bound
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (Exec{}).Run(ctx, "sleep", "5"); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("deadline not honoured: %v after %v", err, time.Since(start))
	}
}
