package junk

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
)

// A Downloads folder that holds a mount point anywhere inside is not moved,
// and is reported; a plain old folder still goes.
func TestOldDownloadsFolderWithMountPointIsSkipped(t *testing.T) {
	f := newFixture(t)
	put(t, f.p("Downloads", "plain", "a.bin"), 10, old)
	put(t, f.p("Downloads", "withmount", "deep", "mnt", "b.bin"), 10, old)
	put(t, f.p("Downloads", "withmount", "a.bin"), 10, old)

	ageDirs(t, f.p("Downloads"))
	mnt := f.p("Downloads", "withmount", "deep", "mnt")
	was := dirDev
	dirDev = func(p string, fi fs.FileInfo) uint64 {
		if p == mnt || filepath.Dir(p) == mnt {
			return was(p, fi) + 1 // another device
		}
		return was(p, fi)
	}
	t.Cleanup(func() { dirDev = was })

	old := f.sc.findOldDownloads(context.Background())
	want := f.p("Downloads", "plain")
	if len(old.paths) != 1 || old.paths[0] != want {
		t.Fatalf("paths = %v, want only %s", old.paths, want)
	}
	if old.failed != 1 {
		t.Fatalf("failed = %d, want the skipped folder reported", old.failed)
	}

	// Reading a folder refreshes its access time on Linux (relatime), which
	// the rule counts as use: age the folders again before the real clean.
	ageDirs(t, f.p("Downloads"))
	res, err := f.sc.Clean(context.Background(), []string{"old-downloads"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.trashed) != 1 || f.trashed[0] != want {
		t.Fatalf("trashed = %v, want only %s", f.trashed, want)
	}
	if res.Failed != 1 {
		t.Fatalf("res = %+v, want the skipped folder counted", res)
	}
}
