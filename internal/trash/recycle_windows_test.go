//go:build windows

package trash

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestWindowsFolderHoldingAJunctionIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "box"), 0o755)
	os.WriteFile(filepath.Join(dir, "box", "a.bin"), make([]byte, 10), 0o644)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "box", "link"), os.TempDir()).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	if size, crossed := sizeOf(context.Background(), filepath.Join(dir, "box")); !crossed || size != 10 {
		t.Fatalf("size %d crossed %v: a folder holding a junction must be left alone", size, crossed)
	}
}
