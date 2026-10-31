// Package recheck decides which of the requested files may still be moved to
// the Trash: only files that are still what a scan saw (same size and
// modification time, still regular files) and, for duplicates, only when a
// copy that stays is still there and still holds the same content.
//
// It does no I/O beyond Lstat and the injected hash, and decides nothing about
// safety: the Trasher's own path guard still runs on whatever it allows.
package recheck

import (
	"context"
	"fmt"
	"slices"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/trash"
	"dev.agus.bersihin/internal/walk"
)

// HashFunc returns the SHA-256 (hex) of a file's whole content; production
// passes dupes.FullHash. It must stop early and fail when ctx ends.
type HashFunc func(ctx context.Context, path string) (string, error)

// Result is the verdict.
type Result struct {
	// Allow lists the requested paths that may be moved, in request order.
	Allow []string
	// Identity is what each allowed path looked like when it was checked;
	// hand it to trash.Trasher.TrashChecked so a later swap is caught.
	Identity map[string]trash.Identity
	// Failed counts requested paths that were refused or not checked.
	Failed int
	// Lines say why, in words for the page.
	Lines []string
	// Cancelled: ctx ended during the check. Nothing is allowed then.
	Cancelled bool
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// same reports whether p is still a regular file of this size and modification time.
func same(p string, size, mtime int64) bool {
	_, ok := stat(p, size, mtime)
	return ok
}

func stat(p string, size, mtime int64) (trash.Identity, bool) {
	fi, err := walk.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
		return trash.Identity{}, false
	}
	m, _, _ := walk.Times(fi)
	return trash.IdentityOf(fi), m == mtime
}

// tally counts the refusals by reason.
type tally struct{ notInScan, changed, noKept int }

func (t tally) result(allow []string, ids map[string]trash.Identity) Result {
	r := Result{Allow: allow, Identity: ids, Failed: t.notInScan + t.changed + t.noKept}
	if t.notInScan > 0 {
		r.Lines = append(r.Lines, plural(t.notInScan, "item was", "items were")+" not part of that scan")
	}
	if t.changed > 0 {
		r.Lines = append(r.Lines, plural(t.changed, "file", "files")+" changed since the scan")
	}
	if t.noKept > 0 {
		r.Lines = append(r.Lines, plural(t.noKept, "file", "files")+" not moved: no copy would have been left")
	}
	return r
}

// Check judges paths against the snapshot. Paths are compared as exact strings.
func Check(ctx context.Context, snap *snapshot.Snap, paths []string, hash HashFunc, rep progress.Reporter) Result {
	var unique []string
	for _, p := range paths {
		if !slices.Contains(unique, p) {
			unique = append(unique, p)
		}
	}
	var t tally
	if snap == nil {
		t.notInScan = len(unique)
		return t.result(nil, nil)
	}
	switch snap.Task {
	case model.TaskLarge:
		var allow []string
		ids := map[string]trash.Identity{}
		for _, p := range unique {
			st, ok := snap.Large[p]
			if !ok {
				t.notInScan++
				continue
			}
			if id, same := stat(p, st.Size, st.MTime); same {
				allow = append(allow, p)
				ids[p] = id
			} else {
				t.changed++
			}
		}
		return t.result(allow, ids)
	case model.TaskDupes:
		return checkDupes(ctx, snap, unique, hash, rep)
	}
	t.notInScan = len(unique)
	return t.result(nil, nil)
}

func checkDupes(ctx context.Context, snap *snapshot.Snap, unique []string, hash HashFunc, rep progress.Reporter) Result {
	var t tally
	// requested paths per group, in request order
	byGroup := map[int][]string{}
	var order []int
	for _, p := range unique {
		gi, ok := snap.DupeAt[p]
		if !ok {
			t.notInScan++
			continue
		}
		if _, seen := byGroup[gi]; !seen {
			order = append(order, gi)
		}
		byGroup[gi] = append(byGroup[gi], p)
	}

	// Stopping allows nothing: the user asked to stop.
	stopped := Result{Cancelled: true, Failed: len(unique)}
	allowed := map[string]bool{}
	ids := map[string]trash.Identity{}
	for i, gi := range order {
		if ctx.Err() != nil {
			return stopped
		}
		g := snap.Dupes[gi]
		req := byGroup[gi]
		var kept []snapshot.Member
		for _, m := range g.Members {
			if !slices.Contains(req, m.Path) {
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			t.noKept += len(req)
			continue
		}
		// the files to move must be unchanged themselves
		var fine []string
		for _, p := range req {
			if id, ok := stat(p, g.Size, mtimeOf(g, p)); ok {
				fine = append(fine, p)
				ids[p] = id
			} else {
				t.changed++
			}
		}
		if len(fine) == 0 {
			continue
		}
		// at least one copy that stays must still hold the content seen by the scan
		verified := false
		for _, m := range kept {
			if hash == nil || !same(m.Path, g.Size, m.MTime) {
				continue
			}
			rep.Call("Checking copies", i, len(order), m.Path)
			h, err := hash(ctx, m.Path)
			if ctx.Err() != nil {
				return stopped
			}
			if err == nil && h == g.Hash {
				verified = true
				break
			}
		}
		if !verified {
			t.changed += len(fine)
			continue
		}
		for _, p := range fine {
			allowed[p] = true
		}
	}
	rep.Call("Checking copies", len(order), len(order), "")
	var allow []string
	for _, p := range unique {
		if allowed[p] {
			allow = append(allow, p)
		}
	}
	return t.result(allow, ids)
}

// mtimeOf is the modification time the scan saw for the member p of g.
func mtimeOf(g snapshot.Group, p string) int64 {
	for _, m := range g.Members {
		if m.Path == p {
			return m.MTime
		}
	}
	return -1
}
