package junk

import (
	"path/filepath"
	"strings"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
)

// The Windows categories are data like the macOS ones: WindowsDefs runs on
// any OS, so its tests do too.

// winLocal is the profile's Local AppData folder: %LOCALAPPDATA% when it is
// set, else its usual place in the profile.
func winLocal(home string, env func(string) string) string {
	if p := env("LOCALAPPDATA"); p != "" && filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(home, "AppData", "Local")
}

// winRootAllowed keeps every cache root clear of the Microsoft folders of
// AppData, which hold the credential and key stores: below Local\Microsoft
// only Edge's own cache folders are cleaned, below Roaming nothing.
func winRootAllowed(home, local, p string) bool {
	g := pathsafe.Guard{FoldCase: true}
	for _, ms := range []string{
		filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "LocalLow", "Microsoft"),
		filepath.Join(local, "Microsoft"), filepath.Join(home, "AppData", "Local", "Microsoft"),
	} {
		if g.Same(ms, p) || g.Inside(p, ms) {
			return false
		}
		if g.Inside(ms, p) && !g.Inside(filepath.Join(local, "Microsoft", "Edge", "User Data"), p) {
			return false
		}
	}
	return true
}

// WindowsDefs returns the categories of a Windows profile whose folder is
// home, with env for %LOCALAPPDATA%. Every root lives in the profile, is a
// real folder and not a link or junction. The Temp folder is only ever
// LocalAppData\Temp: %TEMP% and %TMP% are not read, so neither one set to
// C:\Windows\Temp (admin only) nor one set to Downloads or Documents can
// make their files be deleted for good. The rules are those of the Mac: cache files newer than a day
// stay, virtual environments stay, old downloads go to the Recycle Bin.
func WindowsDefs(home string, env func(string) string) []Def {
	local := winLocal(home, env)
	in := func(p ...string) string { return filepath.Join(append([]string{local}, p...)...) }

	dirs := func(paths []string) []Root {
		var out []Root
		seen := map[string]bool{}
		for _, p := range paths {
			if p == "" || !filepath.IsAbs(p) {
				continue
			}
			k := strings.ToLower(filepath.Clean(p))
			if seen[k] {
				continue
			}
			seen[k] = true
			if winRootAllowed(home, local, p) && validRoot(home, p) {
				out = append(out, Root{Path: p, Keep: keepState})
			}
		}
		return out
	}

	return []Def{
		{
			ID: "win-temp", KeepNewer: keepFresh, Name: "Temporary files",
			Description: "Files apps and installers leave in your Temp folder. Anything from the last day, or still in use, stays.",
			Icon:        model.IconLayers, Safe: true,
			Roots: func() []Root { return dirs([]string{in("Temp")}) },
		},
		{
			ID: "win-crashdumps", KeepNewer: keepFresh, Name: "Crash dumps",
			Description: "Memory dumps Windows saved when apps crashed. Nothing needs them to run.",
			Icon:        model.IconDoc, Safe: true,
			Roots: func() []Root { return dirs([]string{in("CrashDumps")}) },
		},
		{
			ID: "browser-cache", KeepNewer: keepFresh, Name: "Browser caches",
			Description: "Chrome, Edge, Firefox and others rebuild these as you browse. Logins and history stay.",
			Icon:        model.IconGlobe, Safe: true,
			Roots: func() []Root {
				var paths []string
				for _, u := range []string{
					in("Google", "Chrome", "User Data"), in("Microsoft", "Edge", "User Data"),
					in("BraveSoftware", "Brave-Browser", "User Data"), in("Vivaldi", "User Data"),
				} {
					paths = append(paths, chromiumCaches(u)...)
				}
				// Opera keeps a single profile in its own folder
				for _, o := range []string{in("Opera Software", "Opera Stable"), in("Opera Software", "Opera GX Stable")} {
					for _, c := range profileCaches {
						paths = append(paths, filepath.Join(o, c))
					}
				}
				paths = append(paths, firefoxCaches(in("Mozilla", "Firefox", "Profiles"))...)
				return dirs(paths)
			},
		},
		{
			ID: "dev-cache", KeepNewer: keepFresh, Name: "Developer caches",
			Description: "npm, pnpm, Yarn, pip and Gradle caches. The next install downloads packages again.",
			Icon:        model.IconCode, Safe: false,
			Roots: func() []Root {
				return dirs([]string{
					in("npm-cache", "_cacache"), in("pnpm", "store"), in("pnpm-cache"), in("Yarn", "Cache"),
					in("pip", "Cache"), filepath.Join(home, ".gradle", "caches"),
				})
			},
		},
		{
			ID: "old-downloads", Name: "Old downloads",
			Description: "Files in Downloads you have not touched for 90+ days. They go to the Recycle Bin, so you can restore them.",
			Icon:        model.IconDown, Safe: false, Kind: KindDownloads,
		},
	}
}
