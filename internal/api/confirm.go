package api

import (
	"errors"
	"slices"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/snapshot"
)

// Confirm hands out the confirmation tokens that Junk.Clean, Files.TrashPaths,
// Apps.Uninstall and Startup.Set require. Ask for one only after the user
// confirmed. A token works once, for two minutes, for exactly the ids or paths
// it was issued for and the scan they came from.
type Confirm struct{ c *core }

const (
	actionClean     = "clean"
	actionTrash     = "trash"
	actionUninstall = "uninstall"
	actionStartup   = "startup"
)

// maxSubjects bounds one request; a scan never offers more than this.
const maxSubjects = 5000

// Clean returns the token for cleaning these junk category ids of a junk scan.
func (f *Confirm) Clean(scanID string, ids []string) (string, error) {
	snap, err := f.c.snaps.Get(scanID, model.TaskJunk)
	if err != nil {
		return "", err
	}
	if !subsetOf(ids, func(id string) bool { return snap.JunkIDs[id] }) {
		return "", errBadRequest
	}
	return f.c.confirm.Mint(actionClean, scanID, ids)
}

// Trash returns the token for moving these paths of a large files or
// duplicates scan to the Trash. Every path must be one the scan returned.
func (f *Confirm) Trash(scanID string, paths []string) (string, error) {
	snap, err := f.c.fileScan(scanID)
	if err != nil {
		return "", err
	}
	in := func(p string) bool {
		if snap.Task == model.TaskLarge {
			_, ok := snap.Large[p]
			return ok
		}
		_, ok := snap.DupeAt[p]
		return ok
	}
	if !subsetOf(paths, in) {
		return "", errBadRequest
	}
	return f.c.confirm.Mint(actionTrash, scanID, paths)
}

// Uninstall returns the token for uninstalling this app of an app list.
func (f *Confirm) Uninstall(scanID, id string) (string, error) {
	snap, err := f.c.snaps.Get(scanID, model.TaskApps)
	if err != nil {
		return "", err
	}
	if !snap.Apps[id] {
		return "", errBadRequest
	}
	return f.c.confirm.Mint(actionUninstall, scanID, []string{id})
}

// Startup returns the token for turning this login item on or off.
func (f *Confirm) Startup(id string, enabled bool) (string, error) {
	if id == "" {
		return "", errBadRequest
	}
	return f.c.confirm.Mint(actionStartup, "", startupSubjects(id, enabled))
}

// startupSubjects keeps the id and the wanted state apart: the subjects are
// compared as a set, so a bare "true" or "false" could be mistaken for an id.
func startupSubjects(id string, enabled bool) []string {
	if enabled {
		return []string{"id=" + id, "enabled=true"}
	}
	return []string{"id=" + id, "enabled=false"}
}

// subsetOf reports whether list is not empty, not absurdly long, and every
// element satisfies in.
func subsetOf(list []string, in func(string) bool) bool {
	return len(list) > 0 && len(list) <= maxSubjects && !slices.ContainsFunc(list, func(s string) bool { return !in(s) })
}

// fileScan finds the snapshot of a large files or duplicates scan.
func (c *core) fileScan(scanID string) (*snapshot.Snap, error) {
	snap, err := c.snaps.Get(scanID, model.TaskLarge)
	if errors.Is(err, snapshot.ErrKind) {
		snap, err = c.snaps.Get(scanID, model.TaskDupes)
	}
	return snap, err
}
