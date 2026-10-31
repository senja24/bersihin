package junk

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/trash"
	"dev.agus.bersihin/internal/walk"
)

// Scanner measures and cleans the categories of Defs.
type Scanner struct {
	Home    string
	Trasher *trash.Trasher
	Now     func() time.Time
	// TrashEntries, when not nil, limits what a clean of the Trash category
	// removes to the top-level entries a scan saw (name -> device, inode).
	// Anything that appeared since, or was replaced, stays.
	TrashEntries map[string][2]uint64
	// Downloads, when not nil, limits what a clean of old downloads moves to
	// the entries a scan offered, as that scan saw them: an entry that is no
	// longer the same, or that was used since, stays. When nil, a clean judges
	// the folder afresh by the same rule as a scan.
	Downloads map[string]snapshot.Seen
	// LastUsed, when set, returns the macOS "last used" date (epoch ms) of the
	// given files, 0 for a file that has none. An error means the dates could
	// not be read, and the files are then kept.
	LastUsed func(ctx context.Context, paths []string) (map[string]int64, error)
	// Platform picks the categories: "windows" for WindowsDefs, anything
	// else for Defs (the Mac's). Empty is the running OS.
	Platform string
	// Getenv reads the environment for WindowsDefs; default os.Getenv.
	Getenv func(string) string
	// RecycleBin measures and empties the Windows Recycle Bin; default the
	// shell's on Windows, none elsewhere (the category then finds nothing).
	RecycleBin RecycleBin
	// RecycleSeen, when not nil, is the Recycle Bin as a scan saw it: a clean
	// empties it only if it holds no more than that, so nothing deleted
	// since the scan is erased with it.
	RecycleSeen *BinSize
	// ignoreCtime is for tests only: a status change time cannot be set back,
	// so a test that needs an old entry has to leave it out.
	ignoreCtime bool
}

// defs returns the categories of the platform.
func (s *Scanner) defs() []Def {
	platform := s.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform == "windows" {
		env := s.Getenv
		if env == nil {
			env = os.Getenv
		}
		return WindowsDefs(s.Home, env)
	}
	return Defs(s.Home)
}

func (s *Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scanner) display(p string) string { return pathsafe.Tilde(s.Home, p) }

type measured struct {
	size      int64
	count     int
	locations []string
	downloads map[string]snapshot.Seen
	bin       *BinSize
}

func lower(s string) string { return strings.ToLower(s) }

// topOf is the first path element of p below root, lower case.
func topOf(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return lower(first)
}

// measureCache sizes what a clean would remove: every entry inside each
// root, minus excluded and too-fresh files (the same freshness rule as
// DeleteEntry, so the size shown matches what a clean removes).
func (s *Scanner) measureCache(ctx context.Context, def Def, roots []Root, onTick func(string)) measured {
	var cutoff int64
	if def.KeepNewer > 0 {
		cutoff = s.now().Add(-def.KeepNewer).UnixMilli()
	}
	var out measured
	for _, root := range roots {
		excluded := map[string]bool{}
		for _, n := range root.Exclude {
			excluded[lower(n)] = true
		}
		entries := map[string]bool{}
		var bytes int64
		// Hard-linked data is freed only when its last name goes: count it
		// once, and only when every name is inside this scan (as a clean does).
		type linked struct {
			size       int64
			seen, want uint64
		}
		links := map[[2]uint64]*linked{}
		walk.Walk(ctx, root.Path, walk.Options{
			MaxEntries: scanCap,
			SameDevice: true,
			Identity:   true, // the link counts
			SkipDir: func(p, name string, depth int) bool {
				return (depth == 1 && excluded[lower(name)]) || keptByRoot(root, p)
			},
			OnDir: func(p string, depth int) {
				if depth == 1 && cutoff == 0 {
					entries[topOf(root.Path, p)] = true
				}
			},
			OnFile: func(f walk.File) {
				if (f.Depth == 1 && excluded[lower(f.Name)]) || keptByRoot(root, f.Path) {
					return
				}
				if cutoff > 0 && f.Newest() >= cutoff {
					return
				}
				if f.Nlink != 1 {
					key := [2]uint64{f.Dev, f.Ino}
					l := links[key]
					if l == nil {
						l = &linked{size: f.Size, want: f.Nlink}
						links[key] = l
					}
					l.seen++
					if f.Nlink == 0 || l.seen < l.want {
						return
					}
				}
				bytes += f.Size
				entries[topOf(root.Path, f.Path)] = true
			},
			OnTick: func(_ int, current string) { onTick(current) },
		})
		if bytes > 0 {
			out.size += bytes
			out.count += len(entries)
			out.locations = append(out.locations, s.display(root.Path))
		}
	}
	return out
}

// keptByRoot reports whether the root's Keep rule protects p.
func keptByRoot(root Root, p string) bool {
	if root.Keep == nil {
		return false
	}
	rel, err := filepath.Rel(root.Path, p)
	return err != nil || root.Keep(filepath.ToSlash(rel))
}

func touched(mtime, atime, btime int64) int64 { return max(mtime, atime, btime) }

// ctime is the status change time c of an entry, which only ever keeps more:
// it moves when a file is opened by an app that records it (the last-used
// extended attribute), changed or moved into a folder, and cannot be set back.
func (s *Scanner) ctime(c int64) int64 {
	if s.ignoreCtime {
		return 0
	}
	return c
}

type oldDownloads struct {
	paths  []string
	size   int64
	folder string
	// seen is what was seen of each of the paths.
	seen map[string]snapshot.Seen
	// denied and failed count a Downloads folder that could not be listed.
	denied, failed int
	// changed counts offered entries that are no longer what the scan saw, or
	// were used since.
	changed int
}

// dirDev is the device of the folder at p, from its lstat result fi, 0 when
// unknown. A variable so that tests can stand in for a mount.
var dirDev = func(p string, fi fs.FileInfo) uint64 { return walk.Dev(fi) }

// assessed is the verdict on one top-level entry of Downloads.
type assessed struct {
	path string
	// ok: the entry exists and is a regular file or a folder.
	ok   bool
	seen snapshot.Seen
	size int64
	// fresh: used, changed or created within the cutoff, or not fully checked.
	fresh bool
	// crossed: a mount point (or a folder of unknown device) lies inside.
	crossed bool
}

// assess decides whether the entry at p was last touched before cutoff. A
// file counts by its modification, creation, access and status change time. A
// folder counts by its own modification, creation and status change time, the
// same three of every folder inside, and the four times of every file inside;
// never by its own access time, which the reading of the folder (by this scan,
// Spotlight or Finder) updates. A folder with a part that cannot be listed
// counts as in use.
func (s *Scanner) assess(ctx context.Context, p string, cutoff int64) assessed {
	a := assessed{path: p}
	fi, err := walk.Lstat(p)
	if err != nil {
		return a
	}
	m, at, b := walk.Times(fi)
	a.seen = snapshot.Seen{Dev: walk.Dev(fi), Ino: walk.Ino(fi)}
	switch {
	case fi.Mode().IsRegular():
		a.ok = true
		a.size = fi.Size()
		a.seen.Size, a.seen.MTime, a.seen.CTime = fi.Size(), m, s.ctime(walk.CTime(fi))
		a.seen.Newest = max(touched(m, at, b), a.seen.CTime)
		a.fresh = a.seen.Newest >= cutoff
	case fi.IsDir():
		a.ok = true
		a.seen.Dir = true
		a.seen.Newest = max(m, b, s.ctime(walk.CTime(fi)))
		if a.seen.Newest >= cutoff {
			a.fresh = true
			return a
		}
		// A mount point somewhere inside (a network share, a disk image) is
		// not this folder's to move: the folder is left alone.
		rootDev := dirDev(p, fi)
		wctx, cancel := context.WithCancel(ctx)
		files := 0
		st := walk.Walk(wctx, p, walk.Options{
			MaxEntries: 200_000,
			Identity:   true, // the status change times
			OnDirInfo: func(dp string, _ int, di fs.FileInfo) {
				if d := dirDev(dp, di); d != rootDev || d == 0 {
					a.crossed = true
					cancel()
				}
				dm, _, db := walk.Times(di)
				a.seen.Newest = max(a.seen.Newest, dm, db, s.ctime(walk.CTime(di)))
				if a.seen.Newest >= cutoff {
					a.fresh = true
					cancel()
				}
			},
			OnFile: func(f walk.File) {
				a.size += f.Size
				files++
				a.seen.Newest = max(a.seen.Newest, touched(f.MTime, f.ATime, f.BTime), s.ctime(f.CTime))
				if a.seen.Newest >= cutoff {
					a.fresh = true
					cancel()
				}
			},
		})
		cancel()
		a.seen.Bytes, a.seen.Files = a.size, files
		// A folder we could not fully check is treated as in use.
		if st.Truncated || st.Unreadable > 0 || ctx.Err() != nil {
			a.fresh = true
		}
	}
	return a
}

// applyLastUsed raises the newest time of every entry in list, files and
// folders, to its macOS "last used" date. A file's access time misses it once
// that is newer than its modification time, and an app bundle (a folder)
// records it only in Spotlight: launching it changes nothing inside. Entries
// whose date cannot be read are kept.
func (s *Scanner) applyLastUsed(ctx context.Context, list []assessed, cutoff int64) {
	if s.LastUsed == nil {
		return
	}
	var paths []string
	var at []int
	for i, a := range list {
		if a.ok && !a.fresh && !a.crossed {
			paths = append(paths, a.path)
			at = append(at, i)
		}
	}
	if len(paths) == 0 {
		return
	}
	dates, err := s.LastUsed(ctx, paths)
	for _, i := range at {
		if err != nil {
			list[i].fresh = true
			continue
		}
		list[i].seen.Newest = max(list[i].seen.Newest, dates[list[i].path])
		list[i].fresh = list[i].seen.Newest >= cutoff
	}
}

// assessAll runs assess for every path, a few at a time, and then the
// last-used dates.
func (s *Scanner) assessAll(ctx context.Context, paths []string, cutoff int64) []assessed {
	out := make([]assessed, len(paths))
	walk.ForEach(ctx, len(paths), 8, func(i int) { out[i] = s.assess(ctx, paths[i], cutoff) })
	s.applyLastUsed(ctx, out, cutoff)
	return out
}

// findOldDownloads lists the top-level entries of Downloads whose content was
// all last touched (opened, changed or created) 90+ days ago.
func (s *Scanner) findOldDownloads(ctx context.Context) oldDownloads {
	folder := filepath.Join(s.Home, "Downloads")
	res := oldDownloads{folder: folder, seen: map[string]snapshot.Seen{}}
	cutoff := s.now().Add(-oldDownload).UnixMilli()
	list, err := os.ReadDir(folder)
	if err != nil {
		switch statusOf(err) {
		case descDenied:
			res.denied++
		case descFailed:
			res.failed++
		}
		return res
	}
	var cands []string
	for _, d := range list {
		if downloadIgnore[lower(d.Name())] || (!d.IsDir() && !d.Type().IsRegular()) {
			continue
		}
		cands = append(cands, filepath.Join(folder, d.Name()))
	}
	for _, a := range s.assessAll(ctx, cands, cutoff) {
		switch {
		case !a.ok:
		case a.crossed:
			res.failed++
		case !a.fresh:
			res.paths = append(res.paths, a.path)
			res.size += a.size
			res.seen[a.path] = a.seen
		}
	}
	return s.safeOnly(ctx, res)
}

// safeOnly drops the paths the Trash guard refuses.
func (s *Scanner) safeOnly(ctx context.Context, res oldDownloads) oldDownloads {
	slices.Sort(res.paths)
	guard := s.Trasher.Guard(ctx)
	res.paths = slices.DeleteFunc(res.paths, func(p string) bool {
		if guard.Safe(p) {
			return false
		}
		delete(res.seen, p)
		return true
	})
	return res
}

// sameEntry reports whether now is the entry the scan saw as want.
func sameEntry(want, now snapshot.Seen) bool {
	if want.Dir != now.Dir || want.Dev != now.Dev || (want.Ino != 0 && want.Ino != now.Ino) {
		return false
	}
	if want.Dir {
		return want.Bytes == now.Bytes && want.Files == now.Files
	}
	return want.Size == now.Size && want.MTime == now.MTime && want.CTime == now.CTime
}

// recheckDownloads judges the entries the scan offered (s.Downloads) again,
// right before they are moved. An entry stays when it is no longer the same
// entry, when anything in it is newer than what the scan saw, or when it
// cannot be fully checked.
func (s *Scanner) recheckDownloads(ctx context.Context) oldDownloads {
	res := oldDownloads{folder: filepath.Join(s.Home, "Downloads"), seen: map[string]snapshot.Seen{}}
	cutoff := s.now().Add(-oldDownload).UnixMilli()
	paths := make([]string, 0, len(s.Downloads))
	for p := range s.Downloads {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for i, a := range s.assessAll(ctx, paths, cutoff) {
		want := s.Downloads[paths[i]]
		switch {
		case ctx.Err() != nil:
		case !a.ok: // gone: nothing to move
		case a.crossed:
			res.failed++
		case a.fresh || !sameEntry(want, a.seen) || a.seen.Newest > want.Newest:
			res.changed++
		default:
			res.paths = append(res.paths, a.path)
			res.size += a.size
			res.seen[a.path] = a.seen
		}
	}
	return s.safeOnly(ctx, res)
}

func (s *Scanner) measure(ctx context.Context, def Def, onTick func(string)) measured {
	if def.Kind == KindRecycleBin {
		b, ok := s.binSize()
		if !ok {
			return measured{}
		}
		return measured{size: b.Size, count: int(b.Items), locations: []string{"All drives"}, bin: &b}
	}
	if def.Kind == KindDownloads {
		r := s.findOldDownloads(ctx)
		m := measured{size: r.size, count: len(r.paths), downloads: r.seen}
		if len(r.paths) > 0 {
			m.locations = []string{s.display(r.folder)}
		}
		return m
	}
	return s.measureCache(ctx, def, def.Roots(), onTick)
}

// Scan sizes every category and returns the ones that hold something, largest first.
func (s *Scanner) Scan(ctx context.Context, rep progress.Reporter) ([]model.JunkCategory, error) {
	cats, _, err := s.ScanState(ctx, rep)
	return cats, err
}

// trashEntries lists the top-level entries of the Trash category's folders.
func (s *Scanner) trashEntries() map[string][2]uint64 {
	out := map[string][2]uint64{}
	for _, def := range s.defs() {
		if def.ID != "trash" {
			continue
		}
		for _, root := range def.Roots() {
			list, err := os.ReadDir(root.Path)
			if err != nil {
				continue
			}
			for _, e := range list {
				if fi, err := walk.Lstat(filepath.Join(root.Path, e.Name())); err == nil {
					out[e.Name()] = [2]uint64{walk.Dev(fi), walk.Ino(fi)}
				}
			}
		}
	}
	return out
}

// ScanState is Scan that also returns the top-level entries of the Trash
// category's folders as the scan saw them, for Scanner.TrashEntries.
func (s *Scanner) ScanState(ctx context.Context, rep progress.Reporter) ([]model.JunkCategory, map[string][2]uint64, error) {
	cats, st, err := s.ScanWithState(ctx, rep)
	return cats, st.TrashEntries, err
}

// State is what a scan saw that a clean must hold to: see Scanner.TrashEntries
// and Scanner.Downloads.
type State struct {
	TrashEntries map[string][2]uint64
	Downloads    map[string]snapshot.Seen
	// RecycleBin is the Recycle Bin as the scan measured it, nil when it
	// was not measured; see Scanner.RecycleSeen.
	RecycleBin *BinSize
}

// ScanWithState is Scan that also returns the State of the scan.
func (s *Scanner) ScanWithState(ctx context.Context, rep progress.Reporter) ([]model.JunkCategory, State, error) {
	st := State{TrashEntries: s.trashEntries(), Downloads: map[string]snapshot.Seen{}}
	defs := s.defs()
	found := []model.JunkCategory{}
	for i, def := range defs {
		if ctx.Err() != nil {
			return nil, State{}, progress.ErrCancelled
		}
		rep.Call(def.Name, i, len(defs), "")
		m := s.measure(ctx, def, func(cur string) { rep.Call(def.Name, i, len(defs), s.display(cur)) })
		if ctx.Err() != nil {
			return nil, State{}, progress.ErrCancelled
		}
		if m.size <= 0 {
			continue
		}
		if def.Kind == KindDownloads {
			st.Downloads = m.downloads
		}
		if def.Kind == KindRecycleBin {
			st.RecycleBin = m.bin
		}
		found = append(found, model.JunkCategory{
			ID: def.ID, Name: def.Name, Description: def.Description, Icon: def.Icon,
			Size: m.size, Count: m.count, Locations: orEmpty(m.locations, 24), Safe: def.Safe,
			// cache files and the emptied Recycle Bin are gone for good; only old downloads go to the Trash
			Irreversible: def.Kind != KindDownloads,
		})
	}
	rep.Call("Done", len(defs), len(defs), "")
	slices.SortStableFunc(found, func(a, b model.JunkCategory) int { return int(b.Size - a.Size) })
	return found, st, nil
}

func orEmpty(l []string, limit int) []string {
	if len(l) > limit {
		l = l[:limit]
	}
	if l == nil {
		return []string{}
	}
	return l
}

type tally struct {
	freed                            int64
	removed, locked, denied, refused int
	failed                           int
}

func (s *Scanner) cleanCache(ctx context.Context, def Def) tally {
	var t tally
	var mu sync.Mutex
	var cutoff time.Time
	if def.KeepNewer > 0 {
		cutoff = s.now().Add(-def.KeepNewer)
	}
	for _, root := range def.Roots() {
		excluded := map[string]bool{}
		for _, n := range root.Exclude {
			excluded[lower(n)] = true
		}
		list, err := os.ReadDir(root.Path)
		if err != nil {
			st := statusOf(err)
			// Windows reads a file where the folder should be as "path not found"
			if st == descSkip && walk.Exists(root.Path) {
				st = descFailed
			}
			switch st {
			case descDenied:
				t.denied++
			case descFailed:
				t.failed++
			}
			continue
		}
		var names []string
		for _, d := range list {
			if excluded[lower(d.Name())] {
				continue
			}
			if def.ID == "trash" && s.TrashEntries != nil && !s.sameAsScanned(root.Path, d.Name()) {
				continue // arrived or was replaced since the scan
			}
			names = append(names, d.Name())
		}
		walk.ForEach(ctx, len(names), 4, func(i int) {
			r := DeleteEntry(ctx, filepath.Join(root.Path, names[i]), root.Path, DeleteOptions{Home: s.Home, ModifiedBefore: cutoff, Keep: root.Keep})
			mu.Lock()
			t.freed += r.Freed
			t.removed += r.Removed
			t.locked += r.Locked
			t.denied += r.Denied
			t.failed += r.Failed
			if r.Refused {
				t.refused++
			}
			mu.Unlock()
		})
	}
	return t
}

// sameAsScanned reports whether the entry is the one the scan listed under this name.
func (s *Scanner) sameAsScanned(root, name string) bool {
	want, ok := s.TrashEntries[name]
	if !ok {
		return false
	}
	fi, err := walk.Lstat(filepath.Join(root, name))
	return err == nil && want == [2]uint64{walk.Dev(fi), walk.Ino(fi)}
}

func fmtN(n int) string { return fmt.Sprintf("%d", n) }

func files(n int, one, many string) string {
	if n == 1 {
		return fmtN(n) + " " + one
	}
	return fmtN(n) + " " + many
}

// Clean cleans the chosen categories. The page only sends ids: the folders
// are always re-derived here.
func (s *Scanner) Clean(ctx context.Context, ids []string, rep progress.Reporter) (model.CleanResult, error) {
	var defs []Def
	for _, d := range s.defs() {
		if slices.Contains(ids, d.ID) {
			defs = append(defs, d)
		}
	}
	// Old downloads go to the Trash, so they must run after the Trash (or
	// the Recycle Bin) is emptied or they would be erased for good.
	last := func(d Def) int {
		if d.Kind == KindDownloads {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(defs, func(a, b Def) int { return last(a) - last(b) })

	res := model.CleanResult{Skipped: []string{}, Notes: []string{}}
	var locked, refused, failed int
	for i, def := range defs {
		if ctx.Err() != nil {
			break
		}
		rep.Call(def.Name, i, len(defs), "")
		if def.Kind == KindRecycleBin {
			s.emptyBin(&res)
			continue
		}
		if def.Kind == KindDownloads {
			var old oldDownloads
			if s.Downloads != nil {
				old = s.recheckDownloads(ctx)
			} else {
				old = s.findOldDownloads(ctx)
			}
			res.Denied += old.denied
			failed += old.failed
			res.Failed += old.failed + old.changed
			if old.changed > 0 {
				res.Skipped = append(res.Skipped, files(old.changed, "download", "downloads")+" changed or used since the scan")
			}
			// an entry is moved only if it is still the one that was judged
			expect := map[string]trash.Identity{}
			for p, v := range old.seen {
				expect[p] = trash.Identity{Dev: v.Dev, Ino: v.Ino, Size: v.Size, MTime: v.MTime, Dir: v.Dir}
			}
			r := s.Trasher.TrashChecked(ctx, old.paths, expect)
			res.Freed += r.Freed
			res.Removed += r.Removed
			res.Failed += r.Failed
			for _, n := range r.Skipped {
				res.Skipped = append(res.Skipped, "Old downloads: "+n)
			}
			for _, n := range r.Notes {
				res.Notes = append(res.Notes, "Old downloads: "+n)
			}
			if r.Removed > 0 {
				res.Notes = append(res.Notes, "Old downloads went to the "+trash.BinName+", so you can restore them")
			}
			continue
		}
		t := s.cleanCache(ctx, def)
		res.Freed += t.freed
		res.Removed += t.removed
		res.Failed += t.locked + t.failed
		res.Denied += t.denied
		locked += t.locked
		refused += t.refused
		failed += t.failed
	}
	if locked > 0 {
		res.Skipped = append([]string{files(locked, "file", "files") + " in use"}, res.Skipped...)
	}
	if failed > 0 {
		res.Skipped = append(res.Skipped, files(failed, "item", "items")+" could not be cleaned")
	}
	if refused > 0 {
		res.Skipped = append(res.Skipped, files(refused, "linked item", "linked items")+" skipped for safety")
	}
	// what was cleaned stays cleaned: report it rather than fail
	res.Cancelled = ctx.Err() != nil
	rep.Call("Done", len(defs), len(defs), "")
	return res, nil
}
