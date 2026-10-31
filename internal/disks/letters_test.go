package disks

import (
	"context"
	"reflect"
	"testing"
)

func TestWindowsLetterVolumes(t *testing.T) {
	kinds := map[string]uint32{`C:\`: DriveFixed, `D:\`: DriveFixed, `E:\`: DriveRemovable, `F:\`: DriveRemote, `G:\`: DriveCDROM, `R:\`: DriveRAMDisk}
	labels := map[string]string{`D:\`: "Data  "}
	mask := uint32(0)
	for _, l := range "CDEFGR" {
		mask |= 1 << (l - 'A')
	}
	got := LetterVolumes(mask, `c:\Users\ika`, func(r string) uint32 { return kinds[r] }, func(r string) string { return labels[r] })
	want := []Volume{
		{Mount: `C:\`, Label: "Local Disk (C:)", System: true},
		{Mount: `D:\`, Label: "Data (D:)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWindowsSystemRoot(t *testing.T) {
	for in, want := range map[string]string{`C:\Users\ika`: `C:\`, `d:\home`: `D:\`, `\\server\share\u`: "", "/Users/ika": "", "": "", `1:\x`: ""} {
		if got := SystemRoot(in); got != want {
			t.Errorf("SystemRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowsDrivesProbe(t *testing.T) {
	l := &Lister{
		Home: `C:\Users\ika`, Label: "Local Disk (C:)", SystemMount: `C:\`,
		Drives: func(context.Context) ([]Volume, bool) {
			return []Volume{{Mount: `C:\`, Label: "Local Disk (C:)", System: true}, {Mount: `D:\`, Label: "Data (D:)"}}, true
		},
		Stat: func(string) (int64, int64, error) { return 100, 40, nil },
	}
	list, _ := l.List(context.Background())
	if len(list) != 2 || !list[0].System || list[0].Mount != `C:\` || list[1].Mount != `D:\` || list[1].Used != 60 {
		t.Fatalf("List = %+v", list)
	}
	if got := l.DataMounts(context.Background()); !reflect.DeepEqual(got, []string{`D:\`}) {
		t.Fatalf("DataMounts = %v", got)
	}
	sys, err := l.System(context.Background())
	if err != nil || sys.Mount != `C:\` {
		t.Fatalf("System = %+v, %v", sys, err)
	}
}
