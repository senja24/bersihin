// Package trash moves the user's own files to the system Trash (the Recycle
// Bin on Windows), behind the path safety check. Nothing here deletes
// anything for good.
package trash

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/walk"
)

// Trasher moves paths to the Trash.
type Trasher struct {
	// Guard returns the rules for this call (data volumes may change).
	Guard func(ctx context.Context) pathsafe.Guard
	// Move puts one canonical path in the Trash (Shell.TrashItem; on Windows
	// wrapped in RecycleOnly). It must fail, and leave the item alone, when
	// the item cannot be trashed: MyGo's Windows TrashItem recycles through
	// IFileOperation with FOF_ALLOWUNDO and refuses any delete that would
	// not go to the Recycle Bin.
	Move func(path string) error
}

// devOf is the device of the item at p, 0 when unknown. A variable so that
// tests can stand in for a mount.
var devOf = func(p string) uint64 {
	if fi, err := walk.Lstat(p); err == nil {
		return walk.Dev(fi)
	}
	return 0
}

// sizeOf is walk.Size that also reports whether a folder below p lives on
// another device than p (a mounted volume) or on an unknown one. The walk
// never enters such a folder, and the caller must not move p then: the
// Trash move of a folder takes everything mounted in it along.
func sizeOf(ctx context.Context, p string) (size int64, crossed bool) {
	fi, err := walk.Lstat(p)
	if err != nil || !fi.IsDir() {
		return walk.Size(ctx, p), false
	}
	root := walk.Dev(fi)
	st := walk.Walk(ctx, p, walk.Options{
		MaxEntries: 500_000,
		SkipDir: func(dp, _ string, _ int) bool {
			if d := devOf(dp); d != root || d == 0 {
				crossed = true
				return true
			}
			return false
		},
	})
	return st.Bytes, crossed
}

// lockedRe matches the errors of an item held open by another app; on
// Windows the shell reports a sharing or lock violation as an HRESULT.
var lockedRe = regexp.MustCompile(`(?i)in use|busy|being used|locked|sharing violation|HRESULT 0x8007002[01]\b`)

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return commas(n) + " " + one
	}
	return commas(n) + " " + many
}

// Identity is what a scan saw of a regular file or, with Dir set, of a
// folder. TrashChecked compares it right before moving: size and modification
// time in milliseconds as in walk.Times (a folder is told by Dev and Ino
// alone); Dev and Ino are ignored where Ino is 0 (unknown).
type Identity struct {
	Dev, Ino    uint64
	Size, MTime int64
	Dir         bool
}

// IdentityOf reads the identity of fi, which must come from walk.Lstat for
// Dev and Ino to be known on Windows.
func IdentityOf(fi fs.FileInfo) Identity {
	m, _, _ := walk.Times(fi)
	return Identity{Dev: walk.Dev(fi), Ino: walk.Ino(fi), Size: fi.Size(), MTime: m}
}

func (id Identity) matches(fi fs.FileInfo) bool {
	got := IdentityOf(fi)
	if id.Dir {
		return fi.IsDir() && (id.Ino == 0 || (got.Ino == id.Ino && got.Dev == id.Dev))
	}
	if !fi.Mode().IsRegular() || got.Size != id.Size || got.MTime != id.MTime {
		return false
	}
	return id.Ino == 0 || (got.Ino == id.Ino && got.Dev == id.Dev)
}

// Trash moves paths to the Trash and reports what happened. Every path goes
// through the guard twice (as written, then as its canonical path); paths
// that fail either are counted as blocked, never touched. A path inside
// another selected path goes with it.
func (t *Trasher) Trash(ctx context.Context, paths []string) model.CleanResult {
	return t.TrashChecked(ctx, paths, nil)
}

// TrashChecked is Trash that, when expect is not nil, moves a path only if it
// is still the regular file (or folder) expect holds for it (keyed by the path as given):
// the check is made right before the move, so a file swapped after the
// caller's own checks is left alone and counted as failed.
func (t *Trasher) TrashChecked(ctx context.Context, paths []string, expect map[string]Identity) model.CleanResult {
	res := model.CleanResult{Skipped: []string{}, Notes: []string{}}
	g := t.Guard(ctx)

	var unique []string
	for _, p := range paths {
		// exact strings only: a path with ".." is judged (and refused) as written
		if !slices.Contains(unique, p) {
			unique = append(unique, p)
		}
	}
	if len(unique) == 0 {
		return res
	}

	r := pathsafe.NewResolver(g)
	var blocked, gone, locked, errs, changed, mounted int
	var allowed []string
	origin := map[string]string{} // canonical path -> the path as requested
	for _, p := range unique {
		real, v := r.Resolve(p)
		switch v {
		case pathsafe.Gone:
			gone++
		case pathsafe.Blocked:
			blocked++
		default:
			// trash exactly the canonical path that passed; two spellings of one file collapse here
			if !slicesContainsSame(g, allowed, real) {
				allowed = append(allowed, real)
				origin[real] = p
			}
		}
	}
	var list []string
	for _, p := range allowed {
		nested := false
		for _, o := range allowed {
			if o != p && g.Inside(o, p) {
				nested = true
				break
			}
		}
		if !nested {
			list = append(list, p)
		}
	}

	unprocessed := 0
	for i, p := range list {
		if ctx.Err() != nil {
			unprocessed = len(list) - i
			break
		}
		size, crossed := sizeOf(ctx, p)
		if ctx.Err() != nil { // cancelled during the size walk: this one and the rest stay
			unprocessed = len(list) - i
			break
		}
		if crossed {
			mounted++
			continue
		}
		// The size walk takes time: judge the path again right before moving
		// it, and skip it if it is no longer the same safe, real path.
		if again, v := r.Resolve(p); v != pathsafe.OK || again != p {
			blocked++
			continue
		}
		if expect != nil {
			want, ok := expect[origin[p]]
			fi, err := walk.Lstat(p)
			if !ok || err != nil || !want.matches(fi) {
				changed++
				continue
			}
		}
		if err := t.Move(p); err != nil {
			if lockedRe.MatchString(err.Error()) {
				locked++
			} else {
				errs++
			}
			continue
		}
		res.Freed += size
		res.Removed++
	}

	res.Failed = blocked + locked + errs + unprocessed + changed + mounted
	res.Cancelled = unprocessed > 0
	if locked > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s in use", plural(locked, "item is", "items are")))
	}
	if blocked > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s blocked for safety", plural(blocked, "item", "items")))
	}
	if errs > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s could not be moved to the %s", plural(errs, "item", "items"), BinName))
	}
	if changed > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s changed before %s moved", plural(changed, "file", "files"), map[bool]string{true: "it was", false: "they were"}[changed == 1]))
	}
	if mounted > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("%s another volume and %s left alone", plural(mounted, "folder holds", "folders hold"), map[bool]string{true: "was", false: "were"}[mounted == 1]))
	}
	if unprocessed > 0 {
		res.Skipped = append(res.Skipped, fmt.Sprintf("Stopped early: %s not moved", plural(unprocessed, "item was", "items were")))
	}
	if gone > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%s already gone", plural(gone, "item was", "items were")))
	}
	return res
}

func slicesContainsSame(g pathsafe.Guard, list []string, p string) bool {
	for _, q := range list {
		if g.Same(q, p) {
			return true
		}
	}
	return false
}
