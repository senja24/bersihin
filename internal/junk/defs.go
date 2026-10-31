// Package junk finds and cleans the junk categories of a Mac: caches, logs,
// Xcode and developer leftovers, the Trash and old downloads.
//
// Permanent deletion happens only here, only for entries strictly inside the
// roots of a cache category (see DeleteEntry). Old downloads go to the Trash.
package junk

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/walk"
)

// Kind tells how a category is cleaned.
type Kind int

const (
	// KindCache entries are deleted for good, inside the roots.
	KindCache Kind = iota
	// KindDownloads entries go to the Trash.
	KindDownloads
	// KindRecycleBin empties the Windows Recycle Bin of every drive through
	// the shell (Scanner.RecycleBin), which deletes its items for good.
	KindRecycleBin
)

// Root is a folder whose entries a category cleans.
type Root struct {
	Path string
	// Exclude lists entry names directly in this root that belong to another category.
	Exclude []string
	// Keep reports whether the entry at rel (slash separated, relative to Path)
	// holds real state and must be left alone with everything below it.
	Keep func(rel string) bool
}

// Def describes one category.
type Def struct {
	ID, Name, Description string
	Icon                  model.IconName
	Safe                  bool
	Kind                  Kind
	// Roots lists the existing roots, evaluated on demand.
	Roots func() []Root
	// KeepNewer keeps files modified within this duration (fresh files may belong to running apps).
	KeepNewer time.Duration
}

const (
	day         = 24 * time.Hour
	keepFresh   = day
	oldDownload = 90 * day
	scanCap     = 1_500_000
)

var downloadIgnore = map[string]bool{".ds_store": true, ".localized": true, "desktop.ini": true, "thumbs.db": true}

var profileCaches = []string{
	"Cache", "Code Cache", "GPUCache", "DawnCache", "DawnGraphiteCache", "DawnWebGPUCache",
	filepath.Join("Service Worker", "CacheStorage"), filepath.Join("Service Worker", "ScriptCache"),
}
var userDataCaches = []string{"GrShaderCache", "ShaderCache", "GraphiteDawnCache"}
var profileName = regexp.MustCompile(`(?i)^(Default|Profile \d+|Guest Profile|System Profile)$`)

var macBrowserCaches = []string{
	"Google", "BraveSoftware", "Microsoft Edge", "Arc", "company.thebrowser.Browser", "com.apple.Safari", "Firefox", "Vivaldi", "com.operasoftware.Opera",
}
var macDevCaches = []string{"Yarn", "pip", "pnpm"}

// Sync and transfer state rather than caches: iCloud Drive / CloudKit bookkeeping and background downloads in flight.
var macKeepCaches = []string{"com.apple.bird", "CloudKit", "com.apple.cloudd", "com.apple.nsurlsessiond", "com.apple.akd", "FamilyCircle"}

// stateAllowed are com.apple.* cache folders known to be pure caches.
var stateAllowed = map[string]bool{"com.apple.python": true, "com.apple.helpd": true}

// keepState reports whether rel names (or lies in) a folder that holds real
// state although it sits in a cache folder: virtual environments, whatever the
// tool called them.
func keepState(rel string) bool {
	for _, seg := range strings.Split(strings.ToLower(rel), "/") {
		if strings.Contains(seg, "venv") || strings.Contains(seg, "virtualenv") {
			return true
		}
	}
	return false
}

// keepCaches is keepState plus what is known to be state inside
// ~/Library/Caches: poetry's environments, JetBrains local history and the
// com.apple.* folders that are not known pure caches (when in doubt, keep).
func keepCaches(rel string) bool {
	if keepState(rel) {
		return true
	}
	segs := strings.Split(strings.ToLower(rel), "/")
	if strings.HasPrefix(segs[0], "com.apple.") && !stateAllowed[segs[0]] {
		return true
	}
	if segs[0] == "jetbrains" {
		for _, s := range segs[1:] {
			if s == "localhistory" {
				return true
			}
		}
	}
	return false
}

// Defs returns the categories for the user whose home folder is home. Roots
// that are missing, are links, or do not resolve to a place inside home are
// left out.
func Defs(home string) []Def {
	lib := func(p ...string) string { return filepath.Join(append([]string{home, "Library"}, p...)...) }
	support := func(p ...string) string { return lib(append([]string{"Application Support"}, p...)...) }

	dirs := func(paths []string, exclude []string) []Root {
		var out []Root
		seen := map[string]bool{}
		for _, p := range paths {
			k := strings.ToLower(filepath.Clean(p))
			if seen[k] {
				continue
			}
			seen[k] = true
			if validRoot(home, p) {
				out = append(out, Root{Path: p, Exclude: exclude, Keep: keepState})
			}
		}
		return out
	}

	return []Def{
		{
			ID: "mac-caches", KeepNewer: keepFresh, Name: "User caches", Description: "Apps keep these to load faster and rebuild them when needed.",
			Icon: model.IconLayers, Safe: true,
			Roots: func() []Root {
				ex := append(append(append([]string{}, macBrowserCaches...), macDevCaches...), macKeepCaches...)
				roots := dirs([]string{lib("Caches")}, ex)
				for i := range roots {
					roots[i].Keep = keepCaches
				}
				return roots
			},
		},
		{
			ID: "mac-logs", KeepNewer: keepFresh, Name: "Logs", Description: "Diagnostic logs apps write over time. Nothing needs them to run.",
			Icon: model.IconDoc, Safe: true,
			Roots: func() []Root { return dirs([]string{lib("Logs")}, nil) },
		},
		{
			ID: "browser-cache", KeepNewer: keepFresh, Name: "Browser caches", Description: "Chrome, Safari, Firefox and others rebuild these as you browse. Logins and history stay.",
			Icon: model.IconGlobe, Safe: true,
			Roots: func() []Root {
				paths := []string{
					lib("Caches", "Google", "Chrome"), lib("Caches", "BraveSoftware", "Brave-Browser"), lib("Caches", "Microsoft Edge"),
					lib("Caches", "Arc"), lib("Caches", "company.thebrowser.Browser"), lib("Caches", "com.apple.Safari"),
					lib("Caches", "Vivaldi"), lib("Caches", "com.operasoftware.Opera"),
				}
				for _, u := range []string{
					support("Google", "Chrome"), support("BraveSoftware", "Brave-Browser"), support("Microsoft Edge"),
					support("Arc", "User Data"), support("Vivaldi"),
				} {
					paths = append(paths, chromiumCaches(u)...)
				}
				paths = append(paths, firefoxCaches(lib("Caches", "Firefox", "Profiles"))...)
				return dirs(paths, nil)
			},
		},
		{
			ID: "xcode", KeepNewer: keepFresh, Name: "Xcode leftovers", Description: "DerivedData, old device support files and simulator caches. Xcode rebuilds them when needed.",
			Icon: model.IconChip, Safe: false,
			Roots: func() []Root {
				return dirs([]string{
					lib("Developer", "Xcode", "DerivedData"), lib("Developer", "Xcode", "iOS DeviceSupport"), lib("Developer", "CoreSimulator", "Caches"),
				}, nil)
			},
		},
		{
			ID: "dev-cache", KeepNewer: keepFresh, Name: "Developer caches", Description: "npm, Yarn, pnpm and pip caches. The next install downloads packages again.",
			Icon: model.IconCode, Safe: false,
			Roots: func() []Root {
				return dirs([]string{
					filepath.Join(home, ".npm", "_cacache"), lib("Caches", "Yarn"), lib("pnpm", "store"), lib("Caches", "pnpm"),
					lib("Caches", "pip"), filepath.Join(home, ".gradle", "caches"),
				}, nil)
			},
		},
		{
			ID: "trash", Name: "Trash", Description: "Files you already deleted. Emptying it removes them permanently.",
			Icon: model.IconTrash, Safe: false,
			Roots: func() []Root {
				roots := dirs([]string{filepath.Join(home, ".Trash")}, nil)
				for i := range roots {
					roots[i].Keep = nil // emptying the Trash removes everything in it
				}
				return roots
			},
		},
		{
			ID: "old-downloads", Name: "Old downloads",
			Description: "Files in Downloads you have not touched for 90+ days. They go to the Trash, so you can restore them.",
			Icon:        model.IconDown, Safe: false, Kind: KindDownloads,
		},
	}
}

// validRoot: a real folder (not a link) that resolves to a place inside the
// real home folder.
func validRoot(home, p string) bool {
	return walk.IsRealDir(p) && pathsafe.ContainedIn(home, p, true)
}

func subdirs(p string) []string {
	list, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range list {
		if d.IsDir() {
			out = append(out, filepath.Join(p, d.Name()))
		}
	}
	return out
}

// chromiumCaches lists the cache folders of every profile of a Chromium "User Data" folder.
func chromiumCaches(userData string) []string {
	if !walk.IsRealDir(userData) {
		return nil
	}
	var out []string
	for _, c := range userDataCaches {
		out = append(out, filepath.Join(userData, c))
	}
	for _, profile := range subdirs(userData) {
		if profileName.MatchString(filepath.Base(profile)) {
			for _, c := range profileCaches {
				out = append(out, filepath.Join(profile, c))
			}
		}
	}
	return out
}

func firefoxCaches(profiles string) []string {
	var out []string
	for _, p := range subdirs(profiles) {
		out = append(out, filepath.Join(p, "cache2"))
	}
	return out
}
