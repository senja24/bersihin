package junk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/walk"
)

// Outcome is what DeleteEntry did.
type Outcome struct {
	Freed   int64
	Removed int
	// Locked files are held open by another app.
	Locked int
	// Denied files and folders we are not allowed to read or delete (EPERM,
	// EACCES: for example a folder macOS privacy protection closes to us).
	Denied int
	// Failed files and folders that could not be opened, read or deleted for
	// any other reason.
	Failed int
	// Refused: the target failed the realpath / symlink check and was left alone.
	Refused bool
}

// DeleteOptions tune DeleteEntry.
type DeleteOptions struct {
	// Home is the user's home folder. The junk root must resolve to a place
	// strictly inside it; without it nothing is deleted.
	Home string
	// ModifiedBefore keeps files (and folders) created or modified at or after this moment.
	ModifiedBefore time.Time
	// Keep reports whether the entry at rel (slash separated, relative to the
	// root) holds real state and must be left alone with everything below it.
	Keep func(rel string) bool
}

var folded = pathsafe.Guard{FoldCase: true}

// afterScan is called between collecting a tree and deleting it; tests swap
// folders there to prove a swap cannot redirect the deletion.
var afterScan = func() {}

// ctxEvery is how many files are removed between looks at the context.
const ctxEvery = 64

type unlinkResult int

const (
	unlinkOK unlinkResult = iota
	unlinkGone
	unlinkLocked
	unlinkDenied
	unlinkFailed
	unlinkEscaped
)

func classify(err error) unlinkResult {
	switch {
	case err == nil:
		return unlinkOK
	case errors.Is(err, fs.ErrNotExist):
		return unlinkGone
	case errors.Is(err, syscall.EBUSY):
		return unlinkLocked
	case strings.Contains(err.Error(), "escapes from parent"):
		return unlinkEscaped
	case errors.Is(err, fs.ErrPermission):
		return unlinkDenied
	}
	return unlinkFailed
}

// statusOf sorts the error of an open, read or stat: quietly skipped when the
// thing is gone, denied for permission errors (EPERM, EACCES: what a folder
// closed by macOS privacy protection gives), failed for anything else.
func statusOf(err error) descStatus {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return descSkip
	case errors.Is(err, fs.ErrPermission):
		return descDenied
	case strings.Contains(err.Error(), "escapes from parent"):
		return descEscaped
	}
	return descFailed
}

// removed records the result of removing an emptied folder: it stays when
// something fresh or protected is inside (not empty) or it is already gone.
func (o *Outcome) removed(err error) {
	if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return
	}
	o.count(statusOf(err))
}

// count adds a denied or failed status to the outcome and reports whether it did.
func (o *Outcome) count(st descStatus) bool {
	switch st {
	case descDenied:
		o.Denied++
	case descFailed:
		o.Failed++
	default:
		return false
	}
	return true
}

// openRoot opens the junk root once and checks, on the opened folder, that it
// is the real folder inside home that the earlier checks saw. Everything is
// then deleted through the handle, which cannot be redirected by a later swap
// of the root, a parent of it, or a folder below it.
func openRoot(root, home string) (*os.Root, descStatus) {
	if home == "" || !pathsafe.ContainedIn(home, root, true) {
		return nil, descSkip
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		if st := statusOf(err); st == descDenied || st == descFailed {
			return nil, st
		}
		return nil, descSkip
	}
	// the path may have been swapped between the check and the open
	opened, err1 := r.Stat(".")
	now, err2 := os.Lstat(root)
	if err1 != nil || err2 != nil || !os.SameFile(opened, now) || !pathsafe.ContainedIn(home, root, true) {
		r.Close()
		return nil, descSkip
	}
	return r, descOK
}

// descStatus is how descend ended.
type descStatus int

const (
	descOK descStatus = iota
	// descSkip: gone, not a folder, a link or swapped: nothing to report.
	descSkip
	// descDenied: macOS or the permissions closed the folder to us.
	descDenied
	// descFailed: any other error.
	descFailed
	// descEscaped: the handle refused to leave its parent.
	descEscaped
)

// descend opens the folder called name inside h as a handle of its own. name
// is a single path element; a link, a non-folder, or a folder that was swapped
// between the Lstat and the open is refused. Every level thus has its own
// handle, and no multi-part name ever passes through a link.
func descend(h *os.Root, name string) (*os.Root, fs.FileInfo, descStatus) {
	li, err := h.Lstat(name)
	if err != nil {
		return nil, nil, statusOf(err)
	}
	if !li.IsDir() || li.Mode()&fs.ModeSymlink != 0 {
		return nil, nil, descSkip
	}
	c, err := h.OpenRoot(name)
	if err != nil {
		return nil, nil, statusOf(err)
	}
	oi, err := c.Stat(".")
	if err != nil {
		c.Close()
		return nil, nil, statusOf(err)
	}
	if !os.SameFile(li, oi) {
		c.Close()
		return nil, nil, descSkip
	}
	return c, li, descOK
}

type deleter struct {
	ctx     context.Context
	out     *Outcome
	fresh   func(fs.FileInfo) bool
	rootDev uint64
	keep    func(rel string) bool
	n       int
}

func (d *deleter) tally(res unlinkResult, fi fs.FileInfo) {
	switch res {
	case unlinkOK:
		// A name removed while another still points at the data frees nothing;
		// an unknown link count (0) counts as shared.
		if walk.Nlink(fi) == 1 {
			d.out.Freed += fi.Size()
		}
		d.out.Removed++
	case unlinkLocked:
		d.out.Locked++
	case unlinkDenied:
		d.out.Denied++
	case unlinkFailed:
		d.out.Failed++
	case unlinkEscaped:
		d.out.Refused = true
	}
}

// stopped looks at the context every ctxEvery calls.
func (d *deleter) stopped() bool {
	d.n++
	return d.n%ctxEvery == 0 && d.ctx.Err() != nil
}

// clean empties the folder behind h: files are removed, links are left alone,
// folders on another device are left alone, and each subfolder is handled
// through a handle of its own. Subfolders that were not too fresh are removed
// once empty.
func (d *deleter) clean(h *os.Root, top bool, rel string) {
	f, err := h.Open(".")
	if err != nil {
		d.out.count(statusOf(err))
		return
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		d.out.count(statusOf(err)) // what was read is still cleaned
	}
	if top {
		afterScan()
	}
	for _, e := range entries {
		if d.ctx.Err() != nil {
			return
		}
		name := e.Name()
		childRel := rel + "/" + name
		if d.keep != nil && d.keep(childRel) {
			continue
		}
		info, err := h.Lstat(name)
		if err != nil {
			d.out.count(statusOf(err))
			continue
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			// never followed, never removed
		case info.IsDir():
			if walk.Dev(info) != d.rootDev {
				continue // another volume mounted below the root
			}
			child, li, st := descend(h, name)
			if st != descOK {
				if st == descEscaped {
					d.out.Refused = true
				} else {
					d.out.count(st)
				}
				continue
			}
			freshDir := d.fresh(li) // judged before deleting bumps its time
			d.clean(child, false, childRel)
			child.Close()
			if !freshDir {
				d.out.removed(h.Remove(name)) // only succeeds when empty
			}
		case info.Mode().IsRegular():
			if d.fresh(info) {
				continue
			}
			if d.stopped() {
				return
			}
			d.tally(classify(h.Remove(name)), info)
		}
	}
}

// DeleteEntry permanently deletes one cache entry (a file or a folder tree)
// that must live strictly inside root, which must itself be a real folder
// strictly inside Home. Links are never followed: a link as the target is
// refused, links inside are left in place, and so are folders on another
// device than the root. Deletion goes through a handle on the root and then
// one handle per folder, single names only, so a swap after the checks (of
// the root, a parent of it, or a folder below it) cannot reach outside the
// entry, not even into a sibling inside the root. Locked files are skipped
// and counted.
func DeleteEntry(ctx context.Context, target, root string, opt DeleteOptions) Outcome {
	var out Outcome
	refuse := func() Outcome { out.Refused = true; return out }

	rfi, err := os.Lstat(root)
	if err != nil {
		out.count(statusOf(err)) // a root that is gone is nothing to clean
		return out
	}
	if !pathsafe.ContainedIn(root, target, true) || !rfi.IsDir() {
		return refuse()
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return refuse()
	}
	r, st := openRoot(root, opt.Home)
	if st != descOK {
		if out.count(st) {
			return out
		}
		return refuse()
	}
	defer r.Close()
	rootInfo, err := r.Stat(".")
	if err != nil {
		out.count(statusOf(err))
		return out
	}
	rootDev := walk.Dev(rootInfo)

	// down to the parent of the target, one checked handle per level
	parts := strings.Split(rel, string(filepath.Separator))
	h := r
	for _, part := range parts[:len(parts)-1] {
		next, li, st := descend(h, part)
		if st != descOK {
			if out.count(st) {
				return out
			}
			return refuse()
		}
		if walk.Dev(li) != rootDev {
			next.Close()
			return refuse()
		}
		if h != r {
			defer h.Close()
		}
		h = next
	}
	if h != r {
		defer h.Close()
	}
	name := parts[len(parts)-1]

	fi, err := h.Lstat(name)
	if err != nil {
		out.count(statusOf(err))
		return out
	}
	cutoff := opt.ModifiedBefore
	if opt.Keep != nil && opt.Keep(filepath.ToSlash(rel)) {
		return out
	}
	d := &deleter{ctx: ctx, out: &out, rootDev: rootDev, keep: opt.Keep, fresh: func(fi fs.FileInfo) bool {
		m, _, b := walk.Times(fi)
		return !cutoff.IsZero() && max(m, b) >= cutoff.UnixMilli()
	}}

	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return refuse()
	case fi.Mode().IsRegular():
		if !d.fresh(fi) {
			d.tally(classify(h.Remove(name)), fi)
		}
	case fi.IsDir():
		if walk.Dev(fi) != rootDev {
			return refuse() // a volume mounted at the entry itself
		}
		child, li, st := descend(h, name)
		if st != descOK {
			if out.count(st) {
				return out
			}
			return refuse()
		}
		defer child.Close()
		freshTop := d.fresh(li)
		d.clean(child, true, filepath.ToSlash(rel))
		if !freshTop && ctx.Err() == nil {
			out.removed(h.Remove(name))
		}
	}
	return out
}
