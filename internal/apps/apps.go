// Package apps lists the installed applications and removes one: on a Mac by
// moving its bundle to the Trash, on Windows by starting its own uninstaller.
package apps

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/parse"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/walk"
	"dev.agus.bersihin/internal/winreg"
)

// Manager lists apps and remembers which id is which bundle: the page only
// ever sends ids, never paths.
type Manager struct {
	// Run runs plutil, du and mdls. Nil means "not on a Mac": no apps.
	Run  sys.Runner
	Home string
	// Roots are the folders searched, /Applications and ~/Applications.
	Roots []string
	// Trash moves a bundle to the Trash (Shell.TrashItem).
	Trash func(path string) error
	// Logf reports problems worth a log line; default log.Printf.
	Logf func(format string, args ...any)

	// Registry, when set, makes this the Windows Manager: programs come from
	// the Uninstall keys (see windows.go) and Run, Roots and Trash are unused.
	Registry winreg.Registry
	// Launch starts a program's uninstaller (ShellExecute, which shows the
	// UAC prompt when it needs one). Nil: nothing can be uninstalled.
	Launch func(exe, args string) error
	// Getenv expands %SystemRoot% and the like in uninstall commands.
	Getenv func(string) string

	mu    sync.Mutex
	paths map[string]string    // id -> bundle, only apps that may be uninstalled
	win   map[string]winTarget // id -> Uninstall key, on Windows
}

type bundle struct {
	path string
	parse.Bundle
	// unread: Info.plist could not be read, so we cannot tell whether it is
	// one of Apple's. Such an app is never offered for removal.
	unread bool
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// findBundles lists *.app in the roots and one folder below them (vendors
// nest apps, e.g. /Applications/Adobe Photoshop 2025/Adobe Photoshop 2025.app).
func (m *Manager) findBundles() []string {
	var found []string
	for _, root := range m.Roots {
		list, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range list {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			full := filepath.Join(root, e.Name())
			if strings.HasSuffix(e.Name(), ".app") {
				found = append(found, full)
				continue
			}
			if e.Name() == "Utilities" {
				continue
			}
			sub, err := os.ReadDir(full)
			if err != nil {
				continue
			}
			for _, s := range sub {
				if s.IsDir() && strings.HasSuffix(s.Name(), ".app") {
					found = append(found, filepath.Join(full, s.Name()))
				}
			}
		}
	}
	return found
}

func (m *Manager) readBundle(ctx context.Context, appPath string) bundle {
	b := bundle{path: appPath}
	fallback := strings.TrimSuffix(filepath.Base(appPath), ".app")
	b.Name = fallback
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	res, err := m.Run.Run(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(appPath, "Contents", "Info.plist"))
	if err != nil {
		b.unread = true
		return b
	}
	plist, err := parse.PlutilJSON([]byte(res.Stdout))
	if err != nil {
		b.unread = true
		return b
	}
	info := parse.BundleInfo(plist)
	if info.Name != "" {
		b.Name = info.Name
	}
	b.ID, b.Version = info.ID, info.Version
	return b
}

func chunks[T any](s []T, n int) [][]T {
	var out [][]T
	for len(s) > 0 {
		k := min(n, len(s))
		out = append(out, s[:k])
		s = s[k:]
	}
	return out
}

// sizes runs du once per app, each with its own timeout, so one slow or
// failing app leaves the sizes of the others intact.
func (m *Manager) sizes(ctx context.Context, paths []string) map[string]int64 {
	sizes := map[string]int64{}
	var mu sync.Mutex
	walk.ForEach(ctx, len(paths), 4, func(i int) {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, _ := m.Run.Run(cctx, "/usr/bin/du", "-sk", paths[i])
		got := parse.DU(res.Stdout)
		mu.Lock()
		for k, v := range got {
			sizes[k] = v
		}
		mu.Unlock()
	})
	return sizes
}

func (m *Manager) lastUsed(ctx context.Context, paths []string) map[string]int64 {
	dates := map[string]int64{}
	var mu sync.Mutex
	unreadable := 0
	parts := chunks(paths, 50)
	walk.ForEach(ctx, len(parts), 2, func(i int) {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		res, err := m.Run.Run(cctx, "/usr/bin/mdls", append([]string{"-raw", "-name", "kMDItemLastUsedDate"}, parts[i]...)...)
		if err != nil {
			return
		}
		vals, bad := parse.MdlsDatesChecked(res.Stdout, len(parts[i]))
		mu.Lock()
		unreadable += bad
		for j, p := range parts[i] {
			if j < len(vals) && vals[j] > 0 {
				dates[p] = vals[j]
			}
		}
		mu.Unlock()
	})
	if unreadable > 0 {
		logf := m.Logf
		if logf == nil {
			logf = log.Printf
		}
		logf("apps: %d last-used dates could not be read", unreadable)
	}
	return dates
}

// List returns the installed apps sorted by name and refreshes the id table.
func (m *Manager) List(ctx context.Context, rep progress.Reporter) ([]model.InstalledApp, error) {
	if m.Registry != nil {
		return m.listWindows(ctx, rep)
	}
	apps := []model.InstalledApp{}
	if m.Run == nil {
		return apps, nil
	}
	rep.Call("Finding apps", 0, 3, "")
	paths := m.findBundles()
	rep.Call("Reading app details", 1, 3, "")
	bundles := make([]bundle, len(paths))
	walk.ForEach(ctx, len(paths), 8, func(i int) { bundles[i] = m.readBundle(ctx, paths[i]) })
	rep.Call("Measuring apps", 2, 3, "")
	sizes := m.sizes(ctx, paths)
	used := m.lastUsed(ctx, paths)
	if ctx.Err() != nil {
		return nil, progress.ErrCancelled
	}

	table := map[string]string{}
	for _, b := range bundles {
		isApple := strings.HasPrefix(b.ID, "com.apple.")
		id := "mac:" + shortHash(b.path)
		app := model.InstalledApp{
			ID: id, Name: b.Name, Version: b.Version, Size: sizes[b.path], LastUsed: used[b.path],
			Path: b.path, IconPath: b.path, CanUninstall: !isApple && !b.unread,
		}
		if fi, err := os.Lstat(b.path); err == nil {
			_, _, app.Installed = walk.Times(fi)
		}
		if isApple {
			app.Publisher = "Apple"
		} else if !b.unread {
			table[id] = b.path
		}
		apps = append(apps, app)
	}
	slices.SortStableFunc(apps, func(a, b model.InstalledApp) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	m.mu.Lock()
	m.paths = table
	m.mu.Unlock()
	rep.Call("Done", 3, 3, "")
	return apps, nil
}

// Uninstall moves the app with this id to the Trash. Only apps from the last
// List that are not Apple's, still sit in an Applications folder and are real
// bundles qualify.
func (m *Manager) Uninstall(ctx context.Context, id string) model.UninstallResult {
	if m.Registry != nil {
		return m.uninstallWindows(ctx, id)
	}
	if m.Run == nil || m.Trash == nil {
		return model.UninstallResult{Message: "Uninstalling is not supported on this system"}
	}
	m.mu.Lock()
	empty := m.paths == nil
	m.mu.Unlock()
	if empty {
		if _, err := m.List(ctx, nil); err != nil {
			return model.UninstallResult{Message: "This app cannot be removed from Bersihin"}
		}
	}
	m.mu.Lock()
	path, ok := m.paths[id]
	m.mu.Unlock()
	if !ok || !pathsafe.AppBundle(m.Home, path) {
		if ok && !walk.Exists(path) {
			return model.UninstallResult{Message: "The app is already gone"}
		}
		return model.UninstallResult{Message: "This app cannot be removed from Bersihin"}
	}
	if err := m.Trash(path); err != nil {
		return model.UninstallResult{Message: "macOS needs your permission. Drag the app to the Trash in Finder"}
	}
	m.mu.Lock()
	delete(m.paths, id)
	m.mu.Unlock()
	return model.UninstallResult{OK: true, Message: "Moved to Trash. You can put it back from there"}
}
