//go:build windows

package disks

import (
	"context"
	"os"
	"testing"
)

func TestWindowsListsTheSystemDrive(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	l := New(home)
	list, _ := l.List(context.Background())
	if len(list) == 0 || !list[0].System || list[0].Mount != SystemRoot(home) || list[0].Total <= 0 || list[0].Free < 0 {
		t.Fatalf("List = %+v", list)
	}
	for _, m := range l.DataMounts(context.Background()) {
		if m == SystemRoot(home) {
			t.Errorf("the system drive is not a data mount: %v", m)
		}
	}
}
