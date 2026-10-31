package api

import (
	"context"
	"slices"
	"sync/atomic"
	"time"

	"dev.agus.bersihin/internal/dupes"
	"dev.agus.bersihin/internal/junk"
	"dev.agus.bersihin/internal/large"
	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/recheck"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/spaces"
	"dev.agus.bersihin/internal/walk"
)

// noResult is the CleanResult that accompanies an error: never null slices.
func noResult() model.CleanResult { return model.CleanResult{Skipped: []string{}, Notes: []string{}} }

// Junk finds and cleans caches, logs and other leftovers.
type Junk struct{ c *core }

// scanner measures at the fixed moment at, so that the cutoffs of a clean
// (files newer than 24 hours stay, downloads older than 90 days go) are the
// ones the scan showed.
func (j *Junk) scanner(at time.Time) *junk.Scanner {
	return &junk.Scanner{
		Home: j.c.deps.Home, Trasher: j.c.trasher, Now: func() time.Time { return at },
		// a file opened in Preview or Finder (on Windows: through the shell,
		// as Recent Items show) counts as used, whatever its atime says
		LastUsed: lastUsed(j.c.deps.Run),
	}
}

// Scan sizes every junk category and returns the ones that hold something,
// largest first. runID is chosen by the page: progress goes to the "progress"
// event with that id and task "junk", and System.CancelScan(runID) stops the
// scan. The run id is also the scan id that Junk.Clean needs.
func (j *Junk) Scan(ctx context.Context, runID string) ([]model.JunkCategory, error) {
	ctx, rep, end, err := j.c.begin(ctx, runID, model.TaskJunk)
	if err != nil {
		return nil, err
	}
	defer end()
	at := j.c.deps.Now()
	cats, state, err := j.scanner(at).ScanWithState(ctx, rep)
	if err != nil {
		return nil, err
	}
	snap := snapshot.Junk(runID, at, cats)
	snap.TrashEntries = state.TrashEntries
	snap.Downloads = state.Downloads
	if state.RecycleBin != nil {
		snap.RecycleBin = &[2]int64{state.RecycleBin.Size, state.RecycleBin.Items}
	}
	if err := j.c.snaps.Put(snap); err != nil {
		return nil, err
	}
	return cats, nil
}

// Clean cleans the categories with the given ids, which must come from the
// junk scan scanID, with a token from Confirm.Clean for exactly these ids.
// Cache files are deleted for good; old downloads go to the Trash. Progress
// goes to the "progress" event with runID and task "clean".
func (j *Junk) Clean(ctx context.Context, runID, scanID string, ids []string, token string) (model.CleanResult, error) {
	ctx, rep, end, err := j.c.begin(ctx, runID, model.TaskClean)
	if err != nil {
		return noResult(), err
	}
	defer end()
	if err := j.c.confirm.Redeem(token, actionClean, scanID, ids); err != nil {
		return noResult(), err
	}
	snap, err := j.c.snaps.Get(scanID, model.TaskJunk)
	if err != nil {
		return noResult(), err
	}
	for _, id := range ids {
		if !snap.JunkIDs[id] {
			return noResult(), errBadRequest
		}
	}
	sc := j.scanner(snap.At)
	// the Trash is emptied as the scan saw it: whatever arrived since stays
	sc.TrashEntries = snap.TrashEntries
	if sc.TrashEntries == nil {
		sc.TrashEntries = map[string][2]uint64{}
	}
	// old downloads are moved as the scan saw them: whatever changed or was used since stays
	sc.Downloads = snap.Downloads
	if sc.Downloads == nil {
		sc.Downloads = map[string]snapshot.Seen{}
	}
	// the Recycle Bin is emptied only if it holds no more than the scan saw
	sc.RecycleSeen = &junk.BinSize{}
	if b := snap.RecycleBin; b != nil {
		sc.RecycleSeen = &junk.BinSize{Size: b[0], Items: b[1]}
	}
	// old downloads move files to the Trash like TrashPaths; emptying the
	// Recycle Bin must not run while a move puts something new in it
	if slices.Contains(ids, "old-downloads") || slices.Contains(ids, "recycle-bin") {
		unlock, err := j.c.lockMoves(ctx)
		if err != nil {
			return noResult(), err
		}
		defer unlock()
	}
	return sc.Clean(ctx, ids, rep)
}

// Files scans for big and duplicate files and moves files to the Trash.
type Files struct{ c *core }

// ScanSpaces breaks down the home folder (root empty) or a whole volume (its
// mount, from System.Disks) by size, two levels deep. runID is chosen by the page.
func (f *Files) ScanSpaces(ctx context.Context, runID, root string) (model.SpaceNode, error) {
	ctx, rep, end, err := f.c.begin(ctx, runID, model.TaskSpaces)
	if err != nil {
		return model.SpaceNode{}, err
	}
	defer end()
	s := &spaces.Scanner{Home: f.c.deps.Home, Disks: f.c.disks}
	return s.Scan(ctx, root, rep)
}

// trashable returns the Trash guard as a yes or no on a path, so that scans
// list only files that Files.TrashPaths would not refuse.
func (f *Files) trashable(ctx context.Context) func(string) bool {
	r := pathsafe.NewResolver(f.c.trasher.Guard(ctx))
	return func(p string) bool { _, v := r.Resolve(p); return v == pathsafe.OK }
}

// dupeCheckers is how many copies are judged at once: each judgement
// resolves the path (a few stats and a realpath).
const dupeCheckers = 8

// onlyTrashable drops the copies the Trash guard refuses, and the groups left
// with fewer than two copies. It judges the copies in parallel, keeps the
// order, reports progress and stops when ctx ends (progress.ErrCancelled). The
// count is how many copies the page is not shown for this reason.
func onlyTrashable(ctx context.Context, groups []model.DuplicateGroup, allow func(string) bool, rep progress.Reporter) ([]model.DuplicateGroup, int, error) {
	const phase = "Checking which copies can be moved"
	type ref struct{ group, file int }
	var refs []ref
	for gi, g := range groups {
		for fi := range g.Files {
			refs = append(refs, ref{gi, fi})
		}
	}
	ok := make([]bool, len(refs))
	var done atomic.Int64
	rep.Call(phase, 0, len(refs), "")
	walk.ForEach(ctx, len(refs), dupeCheckers, func(i int) {
		path := groups[refs[i].group].Files[refs[i].file].Path
		ok[i] = allow(path)
		rep.Call(phase, int(done.Add(1)), len(refs), path)
	})
	if ctx.Err() != nil {
		return nil, 0, progress.ErrCancelled
	}
	out := []model.DuplicateGroup{}
	shown, n := 0, 0
	for _, g := range groups {
		var keep []model.DuplicateFile
		for _, d := range g.Files {
			if ok[n] {
				keep = append(keep, d)
			}
			n++
		}
		if len(keep) >= 2 {
			g.Files = keep
			out = append(out, g)
			shown += len(keep)
		}
	}
	return out, len(refs) - shown, nil
}

// ScanLargeFiles finds the biggest files in the home folder and on the other
// volumes, at least minBytes each. runID is chosen by the page and is the scan
// id that Files.TrashPaths needs.
func (f *Files) ScanLargeFiles(ctx context.Context, runID string, minBytes int64) (model.LargeScan, error) {
	ctx, rep, end, err := f.c.begin(ctx, runID, model.TaskLarge)
	if err != nil {
		return model.LargeScan{}, err
	}
	defer end()
	allow := f.trashable(ctx)
	s := &large.Scanner{
		Home:  f.c.deps.Home,
		Allow: allow,
		DataMounts: func(ctx context.Context) []model.DiskInfo {
			all, _ := f.c.disks.List(ctx)
			var out []model.DiskInfo
			for _, d := range all {
				if !d.System && d.Mount != "/" {
					out = append(out, d)
				}
			}
			return out
		},
	}
	files, hidden, err := s.ScanCounted(ctx, minBytes, rep)
	if err != nil {
		return model.LargeScan{}, err
	}
	if err := f.c.snaps.Put(snapshot.Large(runID, files)); err != nil {
		return model.LargeScan{}, err
	}
	return model.LargeScan{Files: files, Hidden: hidden}, nil
}

// ScanDuplicates finds identical files of at least 1 MB in the user's own
// folders. runID is chosen by the page and is the scan id that
// Files.TrashPaths needs.
func (f *Files) ScanDuplicates(ctx context.Context, runID string) (model.DuplicateScan, error) {
	ctx, rep, end, err := f.c.begin(ctx, runID, model.TaskDupes)
	if err != nil {
		return model.DuplicateScan{}, err
	}
	defer end()
	const looking = "Looking for files"
	rep.Call(looking, 0, 0, "")
	entries := dupes.Collect(ctx, dupes.Roots(f.c.deps.Home), func(n int, dir string) { rep.Call(looking, n, 0, dir) })
	if ctx.Err() != nil {
		return model.DuplicateScan{}, progress.ErrCancelled
	}
	groups, err := dupes.Find(ctx, entries, rep)
	if err != nil {
		return model.DuplicateScan{}, err
	}
	groups, hidden, err := onlyTrashable(ctx, groups, f.trashable(ctx), rep)
	if err != nil {
		return model.DuplicateScan{}, err
	}
	rep.Call("Done", len(groups), len(groups), "")
	if err := f.c.snaps.Put(snapshot.Dupes(runID, groups)); err != nil {
		return model.DuplicateScan{}, err
	}
	return model.DuplicateScan{Groups: groups, Hidden: hidden}, nil
}

// TrashPaths moves files from the large files or duplicates scan scanID to the
// Trash, where they can be restored, with a token from Confirm.Trash for
// exactly these paths. Each file must still be what the scan saw (same size
// and modification time); for duplicates a copy that stays must still exist
// and hold the same content. Anything else, and anything the safety check
// refuses, is counted, never touched. runID is chosen by the page;
// System.CancelScan(runID) stops the run between files, and the result then
// has cancelled set.
func (f *Files) TrashPaths(ctx context.Context, runID, scanID string, paths []string, token string) (model.CleanResult, error) {
	ctx, rep, end, err := f.c.begin(ctx, runID, model.TaskTrash)
	if err != nil {
		return noResult(), err
	}
	defer end()
	if err := f.c.confirm.Redeem(token, actionTrash, scanID, paths); err != nil {
		return noResult(), err
	}
	snap, err := f.c.fileScan(scanID)
	if err != nil {
		return noResult(), err
	}
	// One move at a time: two overlapping calls could each take the other's
	// file for the copy that stays, and trash every copy of a group.
	unlock, err := f.c.lockMoves(ctx)
	if err != nil {
		return noResult(), err
	}
	defer unlock()
	check := recheck.Check(ctx, snap, paths, f.c.hash, rep)
	if check.Cancelled {
		return model.CleanResult{
			Failed: check.Failed, Skipped: []string{}, Notes: []string{}, Cancelled: true,
		}, nil
	}
	res := f.c.trasher.TrashChecked(ctx, check.Allow, check.Identity)
	res.Failed += check.Failed
	res.Skipped = append(append([]string{}, check.Lines...), res.Skipped...)
	return res, nil
}
