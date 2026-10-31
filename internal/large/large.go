// Package large finds the biggest files in the home folder and on the other
// fixed volumes.
package large

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/walk"
)

const (
	mb           = 1024 * 1024
	maxResults   = 300
	maxEntries   = 2_000_000
	totalEntries = 4_000_000
)

var kinds = map[model.LargeFileKind][]string{
	model.KindVideo:     {"mp4", "mkv", "mov", "avi", "wmv", "flv", "webm", "m4v", "mpg", "mpeg", "3gp", "m2ts", "mts", "vob", "prproj"},
	model.KindArchive:   {"zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "lz", "lzma", "cab"},
	model.KindDiskImage: {"iso", "img", "dmg", "vhd", "vhdx", "vmdk", "vdi", "qcow2", "wim", "esd", "sparseimage", "hdd"},
	model.KindInstaller: {"exe", "msi", "msix", "msixbundle", "appx", "appxbundle", "pkg", "mpkg", "deb", "rpm", "apk", "xapk", "appimage"},
	model.KindAudio:     {"mp3", "wav", "flac", "aac", "m4a", "ogg", "wma", "aiff", "aif", "opus", "alac", "mid"},
	model.KindPhoto:     {"jpg", "jpeg", "png", "gif", "bmp", "tif", "tiff", "heic", "heif", "raw", "cr2", "cr3", "nef", "arw", "dng", "psd", "psb", "webp", "avif", "orf", "rw2", "raf"},
	model.KindDocument:  {"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "txt", "csv", "epub", "mobi", "pages", "numbers", "key", "indd", "ai", "sketch", "fig"},
}

var extKind = func() map[string]model.LargeFileKind {
	m := map[string]model.LargeFileKind{}
	for k, exts := range kinds {
		for _, e := range exts {
			m[e] = k
		}
	}
	return m
}()

// KindOf classifies a file by its extension.
func KindOf(name string) model.LargeFileKind {
	if k, ok := extKind[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]; ok {
		return k
	}
	return model.KindOther
}

// Regenerable caches are the junk scanner's job; they only add noise here.
var cacheDirs = map[string]bool{
	"cache": true, "caches": true, "code cache": true, "gpucache": true, "cache_data": true, "cache2": true,
	"cachestorage": true, "scriptcache": true, "shadercache": true, "grshadercache": true,
}

// Top-level folders of a data volume that hold the OS, installed apps or volume metadata.
var driveSkipTop = map[string]bool{
	"system": true, "library": true, "private": true, "usr": true, "bin": true, "sbin": true, "cores": true,
	"opt": true, "etc": true, "var": true, "backups.backupdb": true, "users": true,
}

// opaqueDir: packages, bundles and git stores are never entered. Their parts
// (bands, VM disks, pack files, app resources) mean nothing alone, and
// removing one breaks the whole.
func opaqueDir(name string) bool {
	return strings.EqualFold(name, ".git") || pathsafe.IsPackageName(name)
}

// opaqueAt is opaqueDir for the folder at p, which also catches bundles of a
// custom type (Contents/Info.plist) and bare git repositories of any name.
func opaqueAt(p, name string) bool { return opaqueDir(name) || pathsafe.IsPackageDir(p) }

// trimAt is how many files are held before the list is cut back to the limit.
var trimAt = maxResults * 10

// candidate turns a walked file into a result when it is big enough.
func candidate(f walk.File, minSize int64) (model.LargeFile, bool) {
	// An iCloud placeholder (macOS) has a size but no blocks here: trashing it
	// frees nothing locally and deletes it from iCloud everywhere.
	if f.Size < minSize || f.IsDataless() {
		return model.LargeFile{}, false
	}
	return model.LargeFile{
		Path: f.Path, Name: f.Name, Size: f.Size, Modified: f.MTime, Accessed: f.ATime, Kind: KindOf(f.Name),
	}, true
}

func realOr(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	return p
}

// Scanner looks for large files.
type Scanner struct {
	Home string
	// DataMounts returns the mounts of the fixed volumes other than the system one, with labels.
	DataMounts func(ctx context.Context) []model.DiskInfo
	// TempDir is skipped; default os.TempDir().
	TempDir string
	// Allow, when set, decides whether a file may be listed (the Trash
	// guard), so that only files that can be moved to the Trash are offered.
	// It runs on candidates only, and the list is still filled to the limit.
	Allow func(path string) bool
}

// Scan returns the biggest files, at least minBytes each (at least 1 MB; a
// smaller value means 100 MB), largest first.
func (s *Scanner) Scan(ctx context.Context, minBytes int64, rep progress.Reporter) ([]model.LargeFile, error) {
	files, _, err := s.ScanCounted(ctx, minBytes, rep)
	return files, err
}

// ScanCounted is Scan that also returns how many big files Allow refused, so
// that the page can say they were left out. Only files that were weighed
// against the limit are counted: a refused file below the cut is not.
func (s *Scanner) ScanCounted(ctx context.Context, minBytes int64, rep progress.Reporter) ([]model.LargeFile, int, error) {
	// refused: the big files Allow turned down that could still have made the list
	var refused []model.LargeFile
	minSize := minBytes
	if minSize < mb {
		minSize = 100 * mb
	}
	g := pathsafe.Guard{FoldCase: true}
	tmp := s.TempDir
	if tmp == "" {
		tmp = os.TempDir()
	}
	library := filepath.Join(s.Home, appData)
	skip := map[string]bool{}
	// ~/Library (AppData on Windows) is left out whole: containers, VM
	// images, app databases and browser profiles live there, and the Trash
	// guard refuses it anyway.
	// Container and VM stores hold disks that break when one file goes.
	skipped := []string{tmp, library, filepath.Join(s.Home, ".Trash"), filepath.Join(s.Home, ".cache")}
	for _, n := range []string{
		".colima", ".lima", ".docker", ".orbstack", "OrbStack",
		"VirtualBox VMs", "Parallels", filepath.Join(".local", "share", "containers"),
		".tart", filepath.Join(".android", "avd"), ".minikube", ".vagrant.d",
	} {
		skipped = append(skipped, filepath.Join(s.Home, n))
	}
	for _, p := range skipped {
		skip[strings.ToLower(filepath.Clean(p))] = true
	}

	var found []model.LargeFile
	bigger := func(a, b model.LargeFile) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	}
	trim := func() {
		slices.SortStableFunc(found, bigger)
		if s.Allow != nil {
			// keep the biggest maxResults files that may be trashed; one that
			// is refused now is refused later, so it is dropped for good
			kept := found[:0]
			for _, f := range found {
				if len(kept) == maxResults {
					break
				}
				if s.Allow(f.Path) {
					kept = append(kept, f)
				} else {
					refused = append(refused, f)
				}
			}
			found = kept
			// a refused file that now ranks below a full list would not have made it
			if len(found) == maxResults {
				last := found[len(found)-1]
				refused = slices.DeleteFunc(refused, func(r model.LargeFile) bool { return bigger(last, r) < 0 })
			}
		}
		if len(found) > maxResults {
			found = found[:maxResults]
		}
	}
	collect := func(f walk.File) {
		lf, ok := candidate(f, minSize)
		if !ok {
			return
		}
		found = append(found, lf)
		if len(found) > trimAt {
			trim()
		}
	}

	seen := 0
	const homePhase = "Looking for large files"
	rep.Call(homePhase, 0, 0, "")
	ticker := func(phase string) func(int, string) {
		return func(entries int, current string) { rep.Call(phase, seen+entries, 0, pathsafe.Tilde(s.Home, current)) }
	}

	st := walk.Walk(ctx, s.Home, walk.Options{
		MaxEntries: maxEntries,
		// A mounted volume inside home (smb, sshfs, FUSE) is not the home folder.
		SameDevice: true,
		// the package check stats the folder: decided outside the walker's lock
		SkipDirConcurrent: func(p, name string, _ int) bool {
			return skip[strings.ToLower(p)] || opaqueAt(p, name) || (cacheDirs[strings.ToLower(name)] && g.Inside(library, p))
		},
		OnFile: collect,
		OnTick: ticker(homePhase),
	})
	seen += st.Files + st.Dirs

	var drives []model.DiskInfo
	if s.DataMounts != nil {
		drives = s.DataMounts(ctx)
	}
	// Home and Library are left out of the drive walk by real path as well:
	// Library may have been moved to a drive and linked back.
	realHome, realLib := realOr(s.Home), realOr(library)
	for _, d := range drives {
		budget := min(maxEntries, totalEntries-seen)
		if budget <= 0 || ctx.Err() != nil {
			break
		}
		phase := "Looking on " + d.Label
		rep.Call(phase, seen, 0, "")
		st := walk.Walk(ctx, d.Mount, walk.Options{
			MaxEntries: budget,
			SameDevice: true,
			SkipDirConcurrent: func(p, name string, depth int) bool {
				return opaqueAt(p, name) || g.Inside(s.Home, p) || g.Same(s.Home, p) ||
					g.Inside(realHome, p) || g.Same(realHome, p) || g.Inside(realLib, p) || g.Same(realLib, p) ||
					(depth == 1 && (driveSkipTop[strings.ToLower(name)] || strings.HasPrefix(name, ".") || strings.HasPrefix(name, driveSkipPrefix)))
			},
			OnFile: collect,
			OnTick: ticker(phase),
		})
		seen += st.Files + st.Dirs
	}
	if ctx.Err() != nil {
		return nil, 0, progress.ErrCancelled
	}

	trim()
	if found == nil {
		found = []model.LargeFile{}
	}
	rep.Call("Done", len(found), len(found), "")
	return found, len(refused), nil
}
