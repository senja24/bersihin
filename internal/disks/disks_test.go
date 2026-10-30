package disks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dev.agus.bersihin/internal/sys"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "macos", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fakeMac(t *testing.T, calls *int) sys.Runner {
	mount, hd := sample(t, "mount.txt"), sample(t, "hdiutil_info.txt")
	return sys.RunFunc(func(_ context.Context, name string, args ...string) (sys.Result, error) {
		*calls++
		switch name {
		case "/sbin/mount":
			return sys.Result{Stdout: mount}, nil
		case "/usr/bin/hdiutil":
			return sys.Result{Stdout: hd}, nil
		}
		return sys.Result{}, errors.New("unexpected command " + name)
	})
}

func sizes(p string) (int64, int64, error) {
	switch p {
	case "/Users/agus":
		return 1000, 400, nil
	case "/Volumes/Data Drive":
		return 4000, 1000, nil
	case "/Volumes/Photos SSD":
		return 2000, 2000, nil
	}
	return 0, 0, errors.New("no such volume")
}

func TestListFromSampleOutput(t *testing.T) {
	var calls int
	l := &Lister{
		Run: fakeMac(t, &calls), Home: "/Users/agus", Label: "Macintosh HD", Stat: sizes,
		IsRealDir: func(string) bool { return true }, Exists: func(string) bool { return false },
	}
	got, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var mounts []string
	for _, d := range got {
		mounts = append(mounts, d.Mount)
	}
	if !slices.Equal(mounts, []string{"/", "/Volumes/Data Drive", "/Volumes/Photos SSD"}) {
		t.Fatalf("mounts = %v", mounts)
	}
	sysd := got[0]
	if !sysd.System || sysd.Label != "Macintosh HD" || sysd.Total != 1000 || sysd.Free != 400 || sysd.Used != 600 {
		t.Errorf("system volume = %+v", sysd)
	}
	if got[1].Label != "Data Drive" || got[1].System || got[1].Used != 3000 {
		t.Errorf("data volume = %+v", got[1])
	}
	if !slices.Equal(l.DataMounts(context.Background()), []string{"/Volumes/Data Drive", "/Volumes/Photos SSD"}) {
		t.Errorf("DataMounts = %v", l.DataMounts(context.Background()))
	}
}

func TestInventoryIsCachedAndExpires(t *testing.T) {
	var calls int
	now := time.Unix(100, 0)
	l := &Lister{
		Run: fakeMac(t, &calls), Home: "/Users/agus", Stat: sizes, Now: func() time.Time { return now },
		IsRealDir: func(string) bool { return true }, Exists: func(string) bool { return false },
	}
	l.List(context.Background())
	first := calls
	l.List(context.Background())
	if calls != first {
		t.Fatalf("second list ran commands again (%d -> %d)", first, calls)
	}
	now = now.Add(31 * time.Second)
	l.List(context.Background())
	if calls == first {
		t.Fatal("cache never expired")
	}
}

func TestVolumeFilters(t *testing.T) {
	l := &Lister{
		Home: "/Users/agus", Stat: sizes,
		IsRealDir: func(p string) bool { return p != "/Volumes/Data Drive" }, // pretend it is a link
		Exists:    func(string) bool { return false },
		Run: sys.RunFunc(func(_ context.Context, name string, _ ...string) (sys.Result, error) {
			if name == "/sbin/mount" {
				return sys.Result{Stdout: sample(t, "mount.txt")}, nil
			}
			return sys.Result{}, nil
		}),
	}
	got, _ := l.List(context.Background())
	for _, d := range got {
		if d.Mount == "/Volumes/Data Drive" {
			t.Error("a volume that is a link was listed")
		}
	}
	// a Time Machine disk by folder
	l2 := &Lister{
		Home: "/Users/agus", Stat: sizes,
		IsRealDir: func(string) bool { return true },
		Exists:    func(p string) bool { return p == "/Volumes/Photos SSD/Backups.backupdb" },
		Run: sys.RunFunc(func(_ context.Context, name string, _ ...string) (sys.Result, error) {
			return sys.Result{Stdout: sample(t, "mount.txt")}, nil
		}),
	}
	got, _ = l2.List(context.Background())
	for _, d := range got {
		if d.Mount == "/Volumes/Photos SSD" {
			t.Error("a Time Machine volume was listed")
		}
	}
}

func TestMountFailureFallsBackAndIsCachedBriefly(t *testing.T) {
	fail := true
	now := time.Unix(100, 0)
	var runs int
	l := &Lister{
		Home: "/Users/agus", Stat: sizes, Now: func() time.Time { return now },
		Run: sys.RunFunc(func(context.Context, string, ...string) (sys.Result, error) {
			runs++
			if fail {
				return sys.Result{}, errors.New("boom")
			}
			return sys.Result{Stdout: sample(t, "mount.txt")}, nil
		}),
		IsRealDir: func(string) bool { return true }, Exists: func(string) bool { return false },
	}
	got, _ := l.List(context.Background())
	if len(got) != 1 || got[0].Mount != "/" {
		t.Fatalf("got %+v", got)
	}
	l.List(context.Background())
	if runs != 1 {
		t.Fatalf("a failure was probed again at once (%d runs)", runs)
	}
	fail = false
	now = now.Add(3 * time.Second)
	got, _ = l.List(context.Background())
	if len(got) != 3 {
		t.Fatalf("failure cached too long: %+v", got)
	}
}

func TestSlowProbeDoesNotBlockOthers(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	l := &Lister{
		Home: "/Users/agus", Stat: sizes,
		Run: sys.RunFunc(func(context.Context, string, ...string) (sys.Result, error) {
			started <- struct{}{}
			<-release
			return sys.Result{Stdout: sample(t, "mount.txt")}, nil
		}),
		IsRealDir: func(string) bool { return true }, Exists: func(string) bool { return false },
	}
	done := make(chan struct{})
	go func() { l.List(context.Background()); close(done) }()
	<-started
	// while the first probe hangs, the lock is free
	locked := make(chan struct{})
	go func() { l.mu.Lock(); l.mu.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock is held while probing")
	}
	close(release)
	<-done
}

func TestUnmeasurableVolumeIsDropped(t *testing.T) {
	l := &Lister{Home: "/Users/agus", Stat: func(string) (int64, int64, error) { return 0, 0, errors.New("x") }}
	got, _ := l.List(context.Background())
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if _, err := l.System(context.Background()); err == nil {
		t.Fatal("System should report the stat error")
	}
}

func TestSystemUsesHome(t *testing.T) {
	l := &Lister{Home: "/Users/agus", Stat: sizes, Label: "Macintosh HD"}
	d, err := l.System(context.Background())
	if err != nil || d.Total != 1000 || d.Used != 600 || !d.System || d.Mount != "/" {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestRealStatfs(t *testing.T) {
	total, free, err := statfs(t.TempDir())
	if err != nil || total <= 0 || free < 0 || free > total {
		t.Fatalf("statfs = %d %d %v", total, free, err)
	}
}

func TestHdiutilFailureIsAFailedProbe(t *testing.T) {
	// a writable disk image must not pass for a fixed volume when hdiutil cannot say which volumes are images
	l := &Lister{
		Home: "/Users/agus", Stat: sizes,
		Run: sys.RunFunc(func(_ context.Context, name string, _ ...string) (sys.Result, error) {
			if name == "/usr/bin/hdiutil" {
				return sys.Result{}, errors.New("hdiutil: timed out")
			}
			return sys.Result{Stdout: sample(t, "mount.txt")}, nil
		}),
		IsRealDir: func(string) bool { return true }, Exists: func(string) bool { return false },
	}
	got, _ := l.List(context.Background())
	if len(got) != 1 || got[0].Mount != "/" {
		t.Fatalf("got %+v", got)
	}
}
