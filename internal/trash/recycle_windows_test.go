//go:build windows

package trash

import (
	"errors"
	"os"
	"testing"
)

func TestWindowsRecycleOnlyRefusesNonFixedDrives(t *testing.T) {
	moved := 0
	move := RecycleOnly(func(string) error { moved++; return nil })
	for _, p := range []string{`\\server\share\a.txt`, `\\?\C:\a.txt`, `relative\a.txt`} {
		if err := move(p); err == nil {
			t.Errorf("%s must be refused", p)
		}
	}
	if moved != 0 {
		t.Fatal("a refused path reached the move")
	}
	home, _ := os.UserHomeDir()
	if err := move(home); err != nil || moved != 1 {
		t.Fatalf("the profile's drive is fixed: %v", err)
	}
	if !lockedRe.MatchString(errors.New("mygo: cannot move x to the recycle bin: mygo: IFileOperation.PerformOperations failed (HRESULT 0x80070020)").Error()) {
		t.Error("a sharing violation counts as in use")
	}
}
