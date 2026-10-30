// Package walk is a parallel, cancellable folder walker that never follows
// symbolic links.
package walk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Extra is the platform specific part of a file's metadata.
type Extra struct {
	Dev, Ino  uint64
	Nlink     uint64 // hard link count, 0 when unknown
	Blocks    int64  // 512-byte blocks allocated
	HasBlocks bool
	// Dataless: macOS marks an iCloud placeholder with SF_DATALESS in st_flags.
	Dataless bool
	ATime    int64 // epoch ms, 0 when unknown
	BTime    int64 // epoch ms, 0 when unknown
	// CTime is the status change time (epoch ms, 0 when unknown): it moves
	// when the file's data, attributes or extended attributes change, or it
	// is moved into another folder, and it cannot be set back.
	CTime int64
}

// File is a regular file seen by the walk. Times are epoch milliseconds.
type File struct {
	Path  string
	Name  string
	Size  int64
	MTime int64
	ATime int64
	BTime int64
	// Depth is 1 for direct children of the walk root.
	Depth int
	Extra
}

// Allocated is the space the file takes on disk: its blocks where the
// platform reports them (an iCloud placeholder takes none), else its size.
func (f File) Allocated() int64 { return allocated(f.Size, f.Extra) }

// IsDataless reports an iCloud placeholder: flagged as such by macOS, or (the
// fallback) a file whose block count is reported and zero.
func (e Extra) IsDataless() bool { return e.Dataless || (e.HasBlocks && e.Blocks == 0) }

func allocated(size int64, e Extra) int64 {
	if e.Dataless {
		return 0
	}
	if e.HasBlocks {
		return e.Blocks * 512
	}
	return size
}

// Allocated is the space fi takes on disk, see File.Allocated.
func Allocated(fi fs.FileInfo) int64 { return allocated(fi.Size(), extra(fi)) }

// Newest is the later of the modification and creation time.
func (f File) Newest() int64 { return max(f.MTime, f.BTime) }

// Options tune a walk. The callbacks are called one at a time (never
// concurrently), from the walker's goroutines.
type Options struct {
	// Concurrency is the number of directories read at once, default 16.
	Concurrency int
	// MaxEntries stops the walk after this many files and folders; 0 = no limit.
	MaxEntries int
	// SameDevice keeps the walk on the root's device: folders that are
	// mount points of another volume are left out.
	SameDevice bool
	OnFile     func(File)
	// OnDir is called for every real (non-link) folder below the root, before its contents.
	OnDir func(path string, depth int)
	// OnDirInfo is OnDir with the folder's own lstat result. It is not called
	// for a folder that could not be stat'ed (that one is counted in
	// Stats.Unreadable).
	OnDirInfo func(path string, depth int, fi fs.FileInfo)
	// SkipDir returns true to leave a folder and everything below it out.
	SkipDir func(path, name string, depth int) bool
	// SkipDirConcurrent is SkipDir for a decision that does I/O: it is called
	// from the walker's goroutines without the lock, so it must be safe for
	// concurrent use. A folder it skips is left out like one SkipDir skips.
	SkipDirConcurrent func(path, name string, depth int) bool
	// OnTick is a heartbeat, roughly every 2,000 entries.
	OnTick func(entries int, current string)
}

// Stats summarise a walk.
type Stats struct {
	Bytes int64
	Files int
	Dirs  int
	// Truncated: MaxEntries was reached before the walk finished.
	Truncated bool
	// Aborted: the context ended the walk.
	Aborted bool
	// Unreadable counts folders that could not be listed and entries that
	// could not be stat'ed: whatever is in them was not seen.
	Unreadable int
}

const tickEvery = 2000

type item struct {
	path  string
	depth int
}

type walker struct {
	ctx  context.Context
	opt  Options
	mu   sync.Mutex // guards everything below, and serialises the callbacks
	cond *sync.Cond

	rootDev  uint64
	realRoot string // where the walk reads
	outRoot  string // how paths are reported
	stack    []item
	pending  int // queued + in flight
	stats    Stats
	entries  int
	nextTick int
}

// Walk walks the tree below root without ever following symbolic links.
// Unreadable entries are skipped, and counted in Stats.Unreadable. The root itself is not reported
// to OnDir. A missing or non-directory root gives an empty result.
func Walk(ctx context.Context, root string, opt Options) Stats {
	w := &walker{ctx: ctx, opt: opt, nextTick: tickEvery}
	w.cond = sync.NewCond(&w.mu)
	fi, err := os.Lstat(root)
	if err != nil {
		return w.stats
	}
	// A root that is a link (a symlinked home) is resolved once, here; links
	// below it are never followed. Paths reported keep the root as given.
	w.outRoot, w.realRoot = root, root
	if fi.Mode()&fs.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return w.stats
		}
		if fi, err = os.Lstat(real); err != nil {
			return w.stats
		}
		w.realRoot = real
	}
	if !fi.IsDir() {
		return w.stats
	}
	w.rootDev = extra(fi).Dev
	w.stack = append(w.stack, item{path: w.realRoot})
	w.pending = 1

	n := opt.Concurrency
	if n < 1 {
		n = 16
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.worker()
		}()
	}
	wg.Wait()
	return w.stats
}

// stopped must be called with w.mu held.
func (w *walker) stopped() bool {
	if w.ctx.Err() != nil {
		w.stats.Aborted = true
	}
	return w.stats.Aborted || w.stats.Truncated
}

func (w *walker) worker() {
	for {
		w.mu.Lock()
		for len(w.stack) == 0 && w.pending > 0 && !w.stopped() {
			w.cond.Wait()
		}
		if w.stopped() || len(w.stack) == 0 {
			w.cond.Broadcast()
			w.mu.Unlock()
			return
		}
		it := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		w.mu.Unlock()

		w.readDir(it)

		w.mu.Lock()
		w.pending--
		if w.pending == 0 || w.stopped() {
			w.cond.Broadcast()
		}
		w.mu.Unlock()
	}
}

// out turns a path below realRoot into the same path below the root as given.
func (w *walker) out(p string) string {
	if w.outRoot == w.realRoot {
		return p
	}
	return filepath.Join(w.outRoot, strings.TrimPrefix(p[len(w.realRoot):], string(filepath.Separator)))
}

func (w *walker) readDir(it item) {
	list, err := os.ReadDir(it.path)
	if err != nil {
		w.noteUnreadable()
		return
	}
	depth := it.depth + 1
	for _, d := range list {
		w.mu.Lock()
		stop := w.stopped()
		w.mu.Unlock()
		if stop {
			return
		}
		full := filepath.Join(it.path, d.Name())
		typ := d.Type()
		switch {
		case typ&fs.ModeSymlink != 0:
			// links are never followed, and never reported
		case typ.IsDir():
			var fi fs.FileInfo
			if w.opt.SameDevice || w.opt.OnDirInfo != nil {
				var err error
				if fi, err = d.Info(); err != nil {
					w.noteUnreadable()
					if w.opt.SameDevice {
						continue
					}
					fi = nil
				} else if w.opt.SameDevice && extra(fi).Dev != w.rootDev {
					continue
				}
			}
			w.enterDir(full, d.Name(), depth, fi)
		case typ.IsRegular():
			fi, err := d.Info() // lstat: the entry itself
			if err != nil {
				w.noteUnreadable()
				continue
			}
			if !fi.Mode().IsRegular() {
				continue
			}
			w.addFile(full, d.Name(), depth, fi)
		}
	}
}

func (w *walker) noteUnreadable() {
	w.mu.Lock()
	w.stats.Unreadable++
	w.mu.Unlock()
}

// count must be called with w.mu held.
func (w *walker) count(p string) {
	w.entries++
	if w.opt.MaxEntries > 0 && w.entries >= w.opt.MaxEntries {
		w.stats.Truncated = true
	}
	if w.opt.OnTick != nil && w.entries >= w.nextTick {
		w.nextTick = w.entries + tickEvery
		w.opt.OnTick(w.entries, w.out(p))
	}
}

func (w *walker) enterDir(p, name string, depth int, fi fs.FileInfo) {
	// decided before taking the lock: it may stat, and the lock serialises every walker
	if w.opt.SkipDirConcurrent != nil && w.opt.SkipDirConcurrent(w.out(p), name, depth) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.opt.SkipDir != nil && w.opt.SkipDir(w.out(p), name, depth) {
		return
	}
	w.stats.Dirs++
	w.count(p)
	if w.opt.OnDir != nil {
		w.opt.OnDir(w.out(p), depth)
	}
	if w.opt.OnDirInfo != nil && fi != nil {
		w.opt.OnDirInfo(w.out(p), depth, fi)
	}
	w.stack = append(w.stack, item{path: p, depth: depth})
	w.pending++
	w.cond.Signal()
}

func (w *walker) addFile(p, name string, depth int, fi fs.FileInfo) {
	ex := extra(fi)
	f := File{
		Path: w.out(p), Name: name, Size: fi.Size(), MTime: fi.ModTime().UnixMilli(),
		ATime: ex.ATime, BTime: ex.BTime, Depth: depth, Extra: ex,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.Files++
	w.stats.Bytes += f.Size
	w.count(p)
	if w.opt.OnFile != nil {
		w.opt.OnFile(f)
	}
}

// Size is the total bytes of a file or folder, links not followed. It looks
// at no more than 500,000 entries; beyond that the figure is a lower bound.
func Size(ctx context.Context, p string) int64 {
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		return 0
	}
	if fi.Mode().IsRegular() {
		return fi.Size()
	}
	return Walk(ctx, p, Options{MaxEntries: 500_000}).Bytes
}

// IsRealDir reports whether p is a folder and not a link.
func IsRealDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

// Exists reports whether anything, a broken link included, is at p.
func Exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// ForEach runs fn for every index below n with at most limit calls in flight
// and waits for all of them. It stops handing out indexes once ctx ends.
func ForEach(ctx context.Context, n, limit int, fn func(i int)) {
	if n <= 0 {
		return
	}
	limit = min(max(limit, 1), n)
	var (
		mu   sync.Mutex
		next int
		wg   sync.WaitGroup
	)
	for range limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n || ctx.Err() != nil {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// Times returns the modification, access and creation time of fi in epoch
// milliseconds (0 where the platform does not say).
func Times(fi fs.FileInfo) (mtime, atime, btime int64) {
	ex := extra(fi)
	return fi.ModTime().UnixMilli(), ex.ATime, ex.BTime
}

// CTime returns the status change time of fi in epoch milliseconds (0 where
// the platform does not say).
func CTime(fi fs.FileInfo) int64 { return extra(fi).CTime }

// Dev returns the device number of a file, 0 when the platform does not say.
func Dev(fi fs.FileInfo) uint64 { return extra(fi).Dev }

// Ino returns the inode number of a file, 0 when the platform does not say.
func Ino(fi fs.FileInfo) uint64 { return extra(fi).Ino }

// Nlink returns the hard link count of a file, 0 when the platform does not say.
func Nlink(fi fs.FileInfo) uint64 { return extra(fi).Nlink }
