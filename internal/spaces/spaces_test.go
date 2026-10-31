package spaces

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/testenv"
	"dev.agus.bersihin/internal/walk"
)

type disks struct{ list []model.DiskInfo }

func (d disks) System(context.Context) (model.DiskInfo, error) { return d.list[0], nil }
func (d disks) List(context.Context) ([]model.DiskInfo, error) { return d.list, nil }

// put writes a file of size bytes and returns what it takes on disk, which is
// what the scanner counts. That is the logical size on Linux but a whole
// number of filesystem blocks on macOS (APFS), so tests compare against the
// return value instead of hard-coding either.
func put(t *testing.T, p string, size int, age time.Duration) int64 {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	testenv.Chtimes(p, ts, ts)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return walk.Allocated(fi)
}

func child(n model.SpaceNode, name string) *model.SpaceNode {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return &n.Children[i]
		}
	}
	return nil
}

const old = 200 * 24 * time.Hour

func TestScanHome(t *testing.T) {
	home := t.TempDir()
	oldDmg := put(t, filepath.Join(home, "Downloads", "old.dmg"), 5000, old)
	newZip := put(t, filepath.Join(home, "Downloads", "new.zip"), 700, time.Hour)
	old2 := put(t, filepath.Join(home, "Downloads", "sub", "old2"), 300, old)
	blob := put(t, filepath.Join(home, "Library", "Caches", "com.foo", "blob"), 4000, time.Hour)
	aLog := put(t, filepath.Join(home, "Library", "Application Support", "Foo", "Logs", "a.log"), 100, time.Hour)
	dataDB := put(t, filepath.Join(home, "Library", "Application Support", "Foo", "data.db"), 900, time.Hour)
	film := put(t, filepath.Join(home, "Movies", "film.mov"), 20000, time.Hour)
	put(t, filepath.Join(home, "Projects", "app", "main.go"), 50, time.Hour)
	notes := put(t, filepath.Join(home, "notes.txt"), 40, time.Hour)
	zshrc := put(t, filepath.Join(home, ".zshrc"), 10, time.Hour)
	outside := t.TempDir()
	put(t, filepath.Join(outside, "huge"), 99999, time.Hour)
	testenv.Symlink(t, outside, filepath.Join(home, "linked"))
	os.MkdirAll(filepath.Join(home, "Empty"), 0o755)

	s := &Scanner{
		Home:  home,
		Disks: disks{[]model.DiskInfo{{Mount: "/", Label: "Macintosh HD", Used: 1_000_000, Total: 2_000_000, System: true}}},
	}
	var phases []string
	got, err := s.Scan(context.Background(), "", progress.Reporter(func(p string, _, _ int, _ string) {
		if len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Home" || got.Path != home || got.Icon != model.IconUser || got.Kind != model.SpaceFolder {
		t.Fatalf("root = %+v", got)
	}
	if child(got, "linked") != nil || child(got, "Empty") != nil {
		t.Error("link or empty folder listed")
	}

	movies := child(got, "Movies")
	if movies == nil || movies.Size != film || movies.Icon != model.IconVideo || len(movies.Children) != 1 || movies.Children[0].Kind != model.SpaceFile {
		t.Fatalf("Movies = %+v", movies)
	}
	dl := child(got, "Downloads")
	if dl == nil || dl.Size != oldDmg+newZip+old2 || dl.Reclaimable != oldDmg+old2 || dl.Icon != model.IconDown {
		t.Fatalf("Downloads = %+v", dl)
	}
	sub := child(*dl, "sub")
	if sub == nil || sub.Kind != model.SpaceFolder || sub.Reclaimable != old2 {
		t.Errorf("sub = %+v", sub)
	}
	lib := child(got, "Library")
	if lib == nil || lib.Size != blob+aLog+dataDB || lib.Reclaimable != blob+aLog || lib.Icon != model.IconLayers {
		t.Fatalf("Library = %+v", lib)
	}
	if p := child(got, "Projects"); p == nil || p.Icon != model.IconCode {
		t.Errorf("Projects = %+v", p)
	}
	loose := child(got, "Loose files")
	if loose == nil || loose.Size != notes+zshrc || len(loose.Children) != 2 {
		t.Fatalf("Loose files = %+v", loose)
	}
	rest := child(got, "System & apps")
	var measured int64
	for _, c := range got.Children {
		if c.Name != "System & apps" {
			measured += c.Size
		}
	}
	if rest == nil || rest.Kind != model.SpaceOther || rest.Size != 1_000_000-measured || rest.Path != "/" {
		t.Fatalf("remainder = %+v (measured %d)", rest, measured)
	}
	if got.Size != 1_000_000 {
		t.Errorf("total = %d", got.Size)
	}
	for i := 1; i < len(got.Children); i++ {
		if got.Children[i-1].Size < got.Children[i].Size {
			t.Errorf("children not sorted: %v", got.Children)
		}
	}
	if len(phases) == 0 || phases[len(phases)-1] != "Done" {
		t.Errorf("phases = %v", phases)
	}
}

func TestMoreItemsBucket(t *testing.T) {
	home := t.TempDir()
	var sizes []int64
	for i := range 20 {
		sizes = append(sizes, put(t, filepath.Join(home, "Documents", "f"+string(rune('a'+i))), (i+1)*10, time.Hour))
	}
	got, err := (&Scanner{Home: home}).Scan(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	docs := child(got, "Documents")
	if docs == nil || len(docs.Children) != maxChildren+1 {
		t.Fatalf("Documents = %+v", docs)
	}
	more := docs.Children[maxChildren]
	if more.Name != "8 more items" || more.Kind != model.SpaceOther || more.Icon != model.IconMore || more.Path != docs.Path {
		t.Fatalf("more = %+v", more)
	}
	// the 8 smallest files (logical 10..80 bytes)
	var want int64
	for _, sz := range sizes[:8] {
		want += sz
	}
	if more.Size != want {
		t.Errorf("more.Size = %d", more.Size)
	}
}

func TestScanDrive(t *testing.T) {
	mount := t.TempDir()
	home := filepath.Join(mount, "Users", "alice")
	aMov := put(t, filepath.Join(home, "Movies", "a.mov"), 3000, time.Hour)
	bobX := put(t, filepath.Join(mount, "Users", "bob", "x"), 400, time.Hour)
	oldZip := put(t, filepath.Join(mount, "Archive", "old.zip"), 2000, time.Hour)
	core := put(t, filepath.Join(mount, "System", "core"), 700, time.Hour)
	put(t, filepath.Join(mount, ".Spotlight-V100", "idx"), 9999, time.Hour)
	put(t, filepath.Join(mount, "Volumes", "mirror"), 9999, time.Hour)
	readme := put(t, filepath.Join(mount, "readme.txt"), 30, time.Hour)

	d := model.DiskInfo{Mount: mount, Label: "Data", Used: 1_000_000, Total: 2_000_000}
	s := &Scanner{Home: home, Disks: disks{[]model.DiskInfo{{Mount: "/", System: true, Used: 1, Total: 2}, d}}}
	got, err := s.Scan(context.Background(), mount, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Data" || got.Path != mount {
		t.Fatalf("root = %+v", got)
	}
	your := child(got, "Your files")
	if your == nil || your.Size != aMov || child(*your, "Movies") == nil {
		t.Fatalf("Your files = %+v", your)
	}
	users := child(got, "Users")
	if users == nil || users.Size != bobX {
		t.Fatalf("Users must exclude the home folder: %+v", users)
	}
	if sys := child(got, "System"); sys == nil || sys.Kind != model.SpaceOther || sys.Icon != model.IconChip {
		t.Errorf("System = %+v", sys)
	}
	if child(got, ".Spotlight-V100") != nil || child(got, "Volumes") != nil {
		t.Error("metadata or mirror folders walked")
	}
	if child(got, "Archive") == nil || child(got, "Loose files") == nil {
		t.Errorf("children = %+v", got.Children)
	}
	rest := child(got, "Hidden & reserved")
	if rest == nil || rest.Size != 1_000_000-(aMov+bobX+oldZip+core+readme) {
		t.Fatalf("remainder = %+v", rest)
	}
}

func TestScanDriveRejectsUnknownMount(t *testing.T) {
	s := &Scanner{Home: t.TempDir(), Disks: disks{[]model.DiskInfo{{Mount: "/", System: true}}}}
	for _, root := range []string{"/Volumes/Nope", "/etc", "/", "relative", "/Volumes/x\x00"} {
		if root == "/" {
			continue // "/" is a listed volume
		}
		if _, err := s.Scan(context.Background(), root, nil); err == nil {
			t.Errorf("Scan(%q) should fail", root)
		}
	}
	if _, err := (&Scanner{Home: t.TempDir()}).Scan(context.Background(), "/Volumes/X", nil); err == nil {
		t.Error("no disk source should fail")
	}
}

func TestScanCancelled(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, "Documents", "a"), 10, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Scanner{Home: home}).Scan(ctx, "", nil); err != progress.ErrCancelled {
		t.Fatalf("err = %v", err)
	}
}

func TestCommas(t *testing.T) {
	if commas(1234567) != "1,234,567" || commas(8) != "8" {
		t.Fatal("commas")
	}
}

// Totals and reclaimable figures count what files take on disk: an iCloud
// placeholder (size but no blocks) counts nothing.
func TestScanCountsAllocatedSizeNotLogical(t *testing.T) {
	old0 := allocatedOf
	allocatedOf = func(f walk.File) int64 {
		if f.Name == "placeholder.mov" {
			return 0
		}
		return f.Size
	}
	defer func() { allocatedOf = old0 }()

	home := t.TempDir()
	put(t, filepath.Join(home, "Movies", "real.mov"), 2000, time.Hour)
	put(t, filepath.Join(home, "Movies", "placeholder.mov"), 9000, time.Hour)
	put(t, filepath.Join(home, "Downloads", "old.dmg"), 500, old)
	put(t, filepath.Join(home, "Downloads", "placeholder.mov"), 7000, old)
	put(t, filepath.Join(home, "Cloud", "placeholder.mov"), 5000, old)
	s := &Scanner{Home: home, Disks: disks{[]model.DiskInfo{{Mount: "/", Label: "Macintosh HD", Used: 1_000_000, Total: 2_000_000, System: true}}}}
	got, err := s.Scan(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := child(got, "Movies"); m == nil || m.Size != 2000 || len(m.Children) != 1 {
		t.Errorf("Movies = %+v", m)
	}
	if d := child(got, "Downloads"); d == nil || d.Size != 500 || d.Reclaimable != 500 {
		t.Errorf("Downloads = %+v", d)
	}
	if c := child(got, "Cloud"); c != nil {
		t.Errorf("a folder of placeholders is listed: %+v", c)
	}
}

// Pins the default wiring (no test swap): a File with block data counts what
// it allocates, a placeholder counts nothing, and without block data (Linux)
// the logical size counts.
func TestDefaultAllocatedOf(t *testing.T) {
	tests := []struct {
		name string
		f    walk.File
		want int64
	}{
		{"blocks win", walk.File{Size: 10_000, Extra: walk.Extra{HasBlocks: true, Blocks: 8}}, 4096},
		{"no blocks: placeholder", walk.File{Size: 10_000, Extra: walk.Extra{HasBlocks: true}}, 0},
		{"flagged dataless", walk.File{Size: 10_000, Extra: walk.Extra{HasBlocks: true, Blocks: 8, Dataless: true}}, 0},
		{"no block data", walk.File{Size: 10_000}, 10_000},
	}
	for _, tc := range tests {
		if got := allocatedOf(tc.f); got != tc.want {
			t.Errorf("%s: allocatedOf = %d, want %d", tc.name, got, tc.want)
		}
	}
}
