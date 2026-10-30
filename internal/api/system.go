package api

import (
	"context"
	"errors"
	"slices"
	"strings"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

// System answers questions about the machine and talks to Finder and the browser.
type System struct{ c *core }

// Info describes the Mac and the app.
func (s *System) Info(ctx context.Context) model.SystemInfo { return s.c.systemInfo(ctx) }

// Disk is the volume the home folder lives on.
func (s *System) Disk(ctx context.Context) (model.DiskInfo, error) { return s.c.disks.System(ctx) }

// Disks lists every fixed local volume, the system volume first.
func (s *System) Disks(ctx context.Context) ([]model.DiskInfo, error) { return s.c.disks.List(ctx) }

// FileIcons returns real Finder icons as PNG data URLs, keyed by the requested
// path; unknown paths are left out. Pass .app bundles, files or folders.
func (s *System) FileIcons(ctx context.Context, paths []string) (map[string]string, error) {
	return s.c.icons.Icons(ctx, paths)
}

// RevealPath shows a file or folder in Finder. "~" stands for the home folder.
func (s *System) RevealPath(path string) error {
	errNo := errors.New("That item cannot be shown")
	if path == "" || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return errNo
	}
	// the same filter as for icons: no network folders, no unknown volumes
	abs := s.c.icons.Candidate(context.Background(), path)
	if abs == "" || !s.c.icons.Present(abs) {
		return errNo
	}
	s.c.deps.Shell.ShowItemInFolder(abs)
	return nil
}

// OpenExternal opens one of the pages in config.ExternalLinks in the default
// browser. Any other link is refused.
func (s *System) OpenExternal(link string) error {
	if !slices.Contains(config.ExternalLinks, link) {
		return errors.New("That link cannot be opened")
	}
	return s.c.deps.Shell.OpenExternal(link)
}

// CancelScan stops the running scan, clean, trash or app list with this run
// id (the one the page passed to that call). It reports whether one was
// running; that call then fails with "scan cancelled" or, for a clean or a
// move to the Trash, returns what was done so far with cancelled set.
func (s *System) CancelScan(runID string) bool { return s.c.scans.Cancel(runID) }
