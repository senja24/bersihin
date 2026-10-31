package large

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/testenv"
	"dev.agus.bersihin/internal/walk"
)

func TestKindOf(t *testing.T) {
	tests := map[string]model.LargeFileKind{
		"movie.MP4": model.KindVideo, "a.mkv": model.KindVideo, "x.zip": model.KindArchive, "x.tar.gz": model.KindArchive,
		"Install.dmg": model.KindDiskImage, "app.pkg": model.KindInstaller, "song.flac": model.KindAudio,
		"IMG_1.HEIC": model.KindPhoto, "report.pdf": model.KindDocument, "notes.txt": model.KindDocument,
		"data.bin": model.KindOther, "noext": model.KindOther, ".hidden": model.KindOther, "weird.": model.KindOther,
		"archive.ZIP": model.KindArchive,
	}
	for name, want := range tests {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func put(t *testing.T, p string, size int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse apart from the last byte: the size is what matters, but the file
	// must own a block, or macOS (APFS) reports no blocks and the scan treats
	// it as an iCloud placeholder.
	if size > 0 {
		if _, err := f.WriteAt([]byte{1}, int64(size)-1); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
}

// putSparse makes a file with a size but, where the platform reports blocks,
// none allocated: what an iCloud placeholder looks like.
func putSparse(t *testing.T, p string, size int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(size)); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestScanSkipsFilesWithoutBlocks(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "Movies", "cloud.mov")
	putSparse(t, p, 300*mb)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if walk.Allocated(fi) != 0 {
		t.Skip("this platform/filesystem allocates blocks for sparse files or reports none")
	}
	got, err := (&Scanner{Home: home, TempDir: filepath.Join(home, "no-tmp")}).Scan(context.Background(), 100*mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("placeholder listed: %+v", got)
	}
}

func TestScan(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	vol := filepath.Join(base, "vol")
	tmp := filepath.Join(base, "tmp")
	put(t, filepath.Join(home, "Movies", "film.mkv"), 300*mb)
	put(t, filepath.Join(home, "Downloads", "image.dmg"), 150*mb)
	put(t, filepath.Join(home, "Downloads", "small.zip"), 10*mb)
	put(t, filepath.Join(home, "Library", "Caches", "com.foo", "huge"), 900*mb)
	put(t, filepath.Join(home, "Library", "Application Support", "Foo", "Cache", "blob"), 800*mb)
	put(t, filepath.Join(home, "Library", "Application Support", "Foo", "keep.db"), 120*mb)
	put(t, filepath.Join(home, "Library", "Containers", "com.docker.docker", "Data", "vms", "Docker.raw"), 950*mb)
	put(t, filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "big.mov"), 950*mb)
	put(t, filepath.Join(home, ".Trash", "old.iso"), 700*mb)
	put(t, filepath.Join(home, ".cache", "x"), 700*mb)
	put(t, filepath.Join(tmp, "t.bin"), 700*mb)
	put(t, filepath.Join(vol, "Archive", "backup.zip"), 400*mb)
	put(t, filepath.Join(vol, "System", "big"), 900*mb)
	put(t, filepath.Join(vol, ".Trashes", "x"), 900*mb)
	put(t, filepath.Join(vol, "Users", "x"), 1*mb)
	testenv.Symlink(t, filepath.Join(vol, "Archive"), filepath.Join(home, "linkToArchive"))

	s := &Scanner{
		Home: home, TempDir: tmp,
		DataMounts: func(context.Context) []model.DiskInfo { return []model.DiskInfo{{Mount: vol, Label: "Data"}} },
	}
	var phases []string
	rep := progress.Reporter(func(p string, _, _ int, _ string) {
		if len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	})
	got, err := s.Scan(context.Background(), 100*mb, rep)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	slices.SortFunc(got, func(a, b model.LargeFile) int { return int(b.Size - a.Size) })
	names = names[:0]
	for _, f := range got {
		names = append(names, f.Name)
	}
	// nothing below ~/Library is offered, not even outside Caches
	wantOrder := []string{"backup.zip", "film.mkv", "image.dmg"}
	if !slices.Equal(names, wantOrder) {
		t.Fatalf("names = %v, want %v", names, wantOrder)
	}
	if got[0].Kind != model.KindArchive || got[1].Kind != model.KindVideo || got[2].Kind != model.KindDiskImage {
		t.Errorf("kinds = %+v", got)
	}
	if got[0].Modified == 0 {
		t.Error("modified not set")
	}
	if !slices.Equal(phases, []string{"Looking for large files", "Looking on Data", "Done"}) {
		t.Errorf("phases = %v", phases)
	}
}

func TestScanMinimumAndLimit(t *testing.T) {
	home := t.TempDir()
	for i := range 320 {
		put(t, filepath.Join(home, "d", string(rune('a'+i%26)), "f"+string(rune('a'+i/26))+".bin"), (i+101)*mb)
	}
	s := &Scanner{Home: home, TempDir: filepath.Join(home, "no-tmp")}
	// 0 means the default of 100 MB, not "everything"
	got, err := s.Scan(context.Background(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 300 {
		t.Fatalf("got %d results", len(got))
	}
	if got[0].Size != 420*mb || got[len(got)-1].Size < 100*mb {
		t.Errorf("first %d last %d", got[0].Size, got[len(got)-1].Size)
	}
	if !slices.IsSortedFunc(got, func(a, b model.LargeFile) int { return int(b.Size - a.Size) }) {
		t.Error("not sorted")
	}
}

func TestScanCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Scanner{Home: t.TempDir()}).Scan(ctx, 100*mb, nil); err != progress.ErrCancelled {
		t.Fatalf("err = %v", err)
	}
}

func TestScanEmptyIsNotNil(t *testing.T) {
	got, err := (&Scanner{Home: t.TempDir()}).Scan(context.Background(), 100*mb, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestCandidateSkipsDatalessFiles(t *testing.T) {
	big := int64(500 * mb)
	tests := []struct {
		name string
		f    walk.File
		want bool
	}{
		{"real file with blocks", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true, Blocks: 1000}}, true},
		{"iCloud placeholder: size but no blocks", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true, Blocks: 0}}, false},
		{"flagged dataless though blocks are reported", walk.File{Name: "a.mov", Size: big, Extra: walk.Extra{HasBlocks: true, Blocks: 1000, Dataless: true}}, false},
		{"platform without block counts keeps the file", walk.File{Name: "a.mov", Size: big}, true},
		{"too small", walk.File{Name: "a.mov", Size: mb, Extra: walk.Extra{HasBlocks: true, Blocks: 1000}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := candidate(tc.f, 100*mb); ok != tc.want {
				t.Errorf("candidate = %v, want %v", ok, tc.want)
			}
		})
	}
}

// The drive walk leaves out the real Library (moved to the drive and linked
// back), the real home, and the OS-owned Users folder: Trash refuses them.
func TestDriveWalkSkipsRealLibraryAndUsers(t *testing.T) {
	// Real mounts are canonical paths; t.TempDir() on macOS sits behind the
	// /var -> /private/var link, which the real-path comparison would trip on.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	vol := filepath.Join(base, "vol")
	os.MkdirAll(home, 0o755)
	put(t, filepath.Join(vol, "Moved", "Library", "Containers", "Docker.raw"), 900*mb)
	put(t, filepath.Join(vol, "Users", "bob", "Library", "Containers", "vm.img"), 900*mb)
	put(t, filepath.Join(vol, "Users", "bob", "Movies", "film.mov"), 900*mb)
	put(t, filepath.Join(vol, "Archive", "backup.zip"), 400*mb)
	if err := testenv.Symlink(t, filepath.Join(vol, "Moved", "Library"), filepath.Join(home, "Library")); err != nil {
		t.Skip(err)
	}
	s := &Scanner{
		Home: home, TempDir: filepath.Join(base, "tmp"),
		DataMounts: func(context.Context) []model.DiskInfo { return []model.DiskInfo{{Mount: vol, Label: "Data"}} },
	}
	got, err := s.Scan(context.Background(), 100*mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"backup.zip"}) {
		t.Fatalf("names = %v, want only backup.zip", names)
	}
}

// Packages, bundles, git stores and container/VM stores are not entered:
// their parts mean nothing alone and removing one breaks the whole.
func TestScanDoesNotEnterPackagesOrVMStores(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	vol := filepath.Join(base, "vol")
	skippedPaths := []string{
		"Backups/disk.sparsebundle/bands/1", "Backups/disk.sparseimage/bands/1", "Movies/Cut.fcpbundle/media/a.mov",
		"Movies/Home.imovielibrary/a.mov", "Music/Song.logicx/Audio Files/a.wav", "Pictures/Photos.photoslibrary/originals/a.heic",
		"Music/Music.musiclibrary/a.m4a", "TV/TV.tvlibrary/a.mov", "VMs/win.pvm/disk.hdd", "VMs/mac.utm/Data/a.qcow2",
		"VMs/lin.vmwarevm/a.vmdk", "Apps/Foo.app/Contents/Resources/big.bin", "Dev/x.bundle/a", "Dev/x.framework/a",
		"Dev/x.xcarchive/a", "Dev/project/.git/objects/pack/pack-1.pack", ".colima/default/colima.qcow2",
		".lima/default/diffdisk", ".docker/desktop/vm.raw", ".orbstack/data/data.img", "OrbStack/data.img",
	}
	for _, p := range skippedPaths {
		put(t, filepath.Join(home, p), 300*mb)
		if !strings.HasPrefix(p, ".") && p != "OrbStack/data.img" { // home-only stores
			put(t, filepath.Join(vol, p), 300*mb)
		}
	}
	put(t, filepath.Join(home, "Movies", "film.mkv"), 300*mb)
	put(t, filepath.Join(vol, "Archive", "backup.zip"), 400*mb)
	s := &Scanner{
		Home: home, TempDir: filepath.Join(base, "tmp"),
		DataMounts: func(context.Context) []model.DiskInfo { return []model.DiskInfo{{Mount: vol, Label: "Data"}} },
	}
	got, err := s.Scan(context.Background(), 100*mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{filepath.Join(vol, "Archive", "backup.zip"), filepath.Join(home, "Movies", "film.mkv")}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// Bare git repositories (by suffix or by shape), VM and container stores in
// home, packages Finder knows by type, and bundles of a custom type are not
// entered.
func TestScanSkipsBareReposMoreStoresAndCustomBundles(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	vol := filepath.Join(base, "vol")
	skipped := []string{
		"Dev/proj.git/objects/pack/pack-1.pack", "Dev/Proj2.GIT/objects/pack/pack-1.pack",
		"VirtualBox VMs/win/win.vdi", "Parallels/mac/disk.hdd", ".local/share/containers/podman/machine/disk.raw",
		"Writing/Novel.scriv/Files/a.rtf", "Data/db.dtBase2/Files.noindex/a.pdf", "Data/db.dtSparse/a.pdf",
		"Photos/Old.migratedphotolibrary/a.heic", "Dev/App.xcodeproj/a.bin", "Dev/p.playground/a.bin", "Dev/x.nib/a.bin",
		"Dev/x.prefPane/a.bin", "Dev/x.saver/a.bin", "Dev/x.cocatalog/a.bin", "Dev/x.lrcat-data/a.bin", "Dev/x.sketch/a.bin",
		"Dev/Custom.thing/Contents/Resources/big.bin",
		"Dev/mirror/objects/pack/pack-1.pack",
	}
	for _, p := range skipped {
		put(t, filepath.Join(home, p), 300*mb)
		if !strings.HasPrefix(p, ".") && !strings.HasPrefix(p, "VirtualBox") && !strings.HasPrefix(p, "Parallels") {
			put(t, filepath.Join(vol, p), 300*mb)
		}
	}
	for _, d := range []string{"Dev/Custom.thing", "Dev/Custom.thing/Contents"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
		os.MkdirAll(filepath.Join(vol, d), 0o755)
	}
	for _, root := range []string{home, vol} {
		put(t, filepath.Join(root, "Dev", "Custom.thing", "Contents", "Info.plist"), 10)
		// a bare repository under another name: HEAD, objects/ and refs/
		put(t, filepath.Join(root, "Dev", "mirror", "HEAD"), 10)
		os.MkdirAll(filepath.Join(root, "Dev", "mirror", "refs"), 0o755)
		// not a repository: no refs/
		put(t, filepath.Join(root, "Dev", "notrepo", "HEAD"), 10)
		put(t, filepath.Join(root, "Dev", "notrepo", "objects", "x.bin"), 300*mb)
	}
	put(t, filepath.Join(home, "Movies", "film.mkv"), 300*mb)
	s := &Scanner{
		Home: home, TempDir: filepath.Join(base, "tmp"),
		DataMounts: func(context.Context) []model.DiskInfo { return []model.DiskInfo{{Mount: vol, Label: "Data"}} },
	}
	got, err := s.Scan(context.Background(), 100*mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{
		filepath.Join(home, "Dev", "notrepo", "objects", "x.bin"), filepath.Join(home, "Movies", "film.mkv"),
		filepath.Join(vol, "Dev", "notrepo", "objects", "x.bin"),
	}
	slices.Sort(paths)
	slices.Sort(want)
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// Emulator and VM stores in home, and the newer package types, are not entered.
func TestScanSkipsEmulatorStoresAndMorePackageTypes(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	for _, p := range []string{
		".tart/vms/mac/disk.img", ".android/avd/Pixel.avd/userdata.img", ".minikube/machines/m/disk.raw", ".vagrant.d/boxes/b/box.vmdk",
		"Dev/m.mlpackage/Data/w.bin", "Dev/x.xcframework/ios/big.a", "Notes/n.textbundle/assets/big.mov",
		"Draw/d.graffle/data.plist", "Contacts/c.abbu/big.bin", "VMs/p.hdd/disk.hds",
		"Dev/linkrepo/objects/pack/p.pack",
	} {
		put(t, filepath.Join(home, p), 300*mb)
	}
	// a bare repository whose HEAD is a link
	put(t, filepath.Join(home, "Dev", "target-ref"), 10)
	os.MkdirAll(filepath.Join(home, "Dev", "linkrepo", "refs"), 0o755)
	if err := testenv.Symlink(t, filepath.Join(home, "Dev", "target-ref"), filepath.Join(home, "Dev", "linkrepo", "HEAD")); err != nil {
		t.Fatal(err)
	}
	// ~/.android itself is not skipped, only its avd folder
	put(t, filepath.Join(home, ".android", "other", "big.bin"), 300*mb)
	put(t, filepath.Join(home, "Movies", "film.mkv"), 300*mb)
	got, err := (&Scanner{Home: home, TempDir: filepath.Join(base, "tmp")}).Scan(context.Background(), 100*mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	slices.Sort(paths)
	want := []string{filepath.Join(home, ".android", "other", "big.bin"), filepath.Join(home, "Movies", "film.mkv")}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// BenchmarkScanFolderHeavy walks many small folders: the package check stats
// each one, which used to happen under the walker's lock.
func BenchmarkScanFolderHeavy(b *testing.B) {
	home := b.TempDir()
	for i := range 40 {
		for j := range 60 {
			os.MkdirAll(filepath.Join(home, "Dev", strconv.Itoa(i), strconv.Itoa(j), "src"), 0o755)
		}
	}
	s := &Scanner{Home: home, TempDir: filepath.Join(home, "no-tmp")}
	b.ResetTimer()
	for range b.N {
		if _, err := s.Scan(context.Background(), 100*mb, nil); err != nil {
			b.Fatal(err)
		}
	}
}
