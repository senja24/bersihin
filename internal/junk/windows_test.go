package junk

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/trash"
)

// winFixture is a Windows profile laid out in this OS's path syntax.
func winFixture(t *testing.T, env map[string]string) *fixture {
	t.Helper()
	f := newFixture(t)
	if env == nil {
		env = map[string]string{}
	}
	f.sc.Platform = "windows"
	f.sc.Getenv = func(k string) string { return env[k] }
	f.sc.Trasher.Guard = func(context.Context) pathsafe.Guard {
		return pathsafe.NewWindowsGuard(f.home, nil, nil, func(k string) string { return env[k] })
	}
	return f
}

func rootsOf(defs []Def, id string) []string {
	var out []string
	for _, d := range defs {
		if d.ID == id && d.Roots != nil {
			for _, r := range d.Roots() {
				out = append(out, r.Path)
			}
		}
	}
	return out
}

func TestWindowsDefsRoots(t *testing.T) {
	f := newFixture(t)
	local := f.p("AppData", "Local")
	for _, d := range []string{
		"Temp", "CrashDumps",
		filepath.Join("Google", "Chrome", "User Data", "Default", "Cache"),
		filepath.Join("Google", "Chrome", "User Data", "Profile 2", "Code Cache"),
		filepath.Join("Google", "Chrome", "User Data", "Default", "Network"), // cookies: never a root
		filepath.Join("Microsoft", "Edge", "User Data", "Default", "Cache"),
		filepath.Join("BraveSoftware", "Brave-Browser", "User Data", "Default", "GPUCache"),
		filepath.Join("Vivaldi", "User Data", "Default", "Cache"),
		filepath.Join("Opera Software", "Opera Stable", "Cache"),
		filepath.Join("Mozilla", "Firefox", "Profiles", "abc.default-release", "cache2"),
		filepath.Join("npm-cache", "_cacache"), filepath.Join("pnpm", "store"), filepath.Join("Yarn", "Cache"),
		filepath.Join("pip", "Cache"),
	} {
		os.MkdirAll(filepath.Join(local, d), 0o755)
	}
	os.MkdirAll(f.p(".gradle", "caches"), 0o755)
	// a %TEMP% outside the profile (C:\Windows\Temp) is never cleaned
	env := map[string]string{"TEMP": filepath.Join(f.outside, "WindowsTemp"), "TMP": filepath.Join(local, "Temp")}
	os.MkdirAll(env["TEMP"], 0o755)
	defs := WindowsDefs(f.home, func(k string) string { return env[k] })

	var ids []string
	for _, d := range defs {
		ids = append(ids, d.ID)
	}
	if !slices.Equal(ids, []string{"win-temp", "win-crashdumps", "browser-cache", "dev-cache", "old-downloads"}) {
		t.Fatalf("ids = %v", ids)
	}
	if got := rootsOf(defs, "win-temp"); !slices.Equal(got, []string{filepath.Join(local, "Temp")}) {
		t.Errorf("temp roots = %v", got)
	}
	if got := rootsOf(defs, "win-crashdumps"); !slices.Equal(got, []string{filepath.Join(local, "CrashDumps")}) {
		t.Errorf("crash dump roots = %v", got)
	}
	browser := rootsOf(defs, "browser-cache")
	for _, want := range []string{
		filepath.Join(local, "Google", "Chrome", "User Data", "Default", "Cache"),
		filepath.Join(local, "Google", "Chrome", "User Data", "Profile 2", "Code Cache"),
		filepath.Join(local, "Microsoft", "Edge", "User Data", "Default", "Cache"),
		filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data", "Default", "GPUCache"),
		filepath.Join(local, "Vivaldi", "User Data", "Default", "Cache"),
		filepath.Join(local, "Opera Software", "Opera Stable", "Cache"),
		filepath.Join(local, "Mozilla", "Firefox", "Profiles", "abc.default-release", "cache2"),
	} {
		if !slices.Contains(browser, want) {
			t.Errorf("browser roots miss %s: %v", want, browser)
		}
	}
	for _, r := range browser {
		if strings.Contains(r, "Network") {
			t.Errorf("a profile's cookies are not a cache: %s", r)
		}
	}
	if got := rootsOf(defs, "dev-cache"); len(got) != 5 {
		t.Errorf("dev roots = %v", got)
	}
	for _, d := range defs {
		if d.ID == "old-downloads" && (d.Kind != KindDownloads || !strings.Contains(d.Description, "Recycle Bin")) {
			t.Errorf("old downloads: %+v", d)
		}
	}
}

func TestWindowsDefsStayOutOfMicrosoftFolders(t *testing.T) {
	home := filepath.FromSlash("/C/Users/ika")
	local := filepath.Join(home, "AppData", "Local")
	for p, want := range map[string]bool{
		filepath.Join(local, "Temp"): true,
		filepath.Join(local, "Microsoft", "Edge", "User Data", "Default", "Cache"):  true,
		filepath.Join(local, "microsoft", "edge", "user data", "Default", "Cache"):  true,
		filepath.Join(local, "Microsoft", "Windows", "INetCache"):                   false,
		filepath.Join(local, "Microsoft", "Credentials"):                            false,
		filepath.Join(local, "Microsoft"):                                           false,
		filepath.Join(home, "AppData", "Roaming", "Microsoft", "Protect"):           false,
		filepath.Join(home, "AppData", "Roaming", "npm-cache"):                      false,
		filepath.Join(home, "AppData", "LocalLow", "Microsoft", "CryptnetUrlCache"): false,
		local:                          false,
		filepath.Join(home, "AppData"): false,
	} {
		if got := winRootAllowed(home, local, p); got != want {
			t.Errorf("winRootAllowed(%s) = %v, want %v", p, got, want)
		}
	}
}

func TestWindowsDefsSkipLinkedRoots(t *testing.T) {
	f := newFixture(t)
	local := f.p("AppData", "Local")
	os.MkdirAll(local, 0o755)
	os.MkdirAll(filepath.Join(f.outside, "elsewhere"), 0o755)
	if err := os.Symlink(filepath.Join(f.outside, "elsewhere"), filepath.Join(local, "Temp")); err != nil {
		t.Skip(err)
	}
	if got := rootsOf(WindowsDefs(f.home, func(string) string { return "" }), "win-temp"); len(got) != 0 {
		t.Errorf("a linked Temp must be left out: %v", got)
	}
}

func TestWindowsScanAndClean(t *testing.T) {
	f := winFixture(t, nil)
	temp := f.p("AppData", "Local", "Temp")
	put(t, filepath.Join(temp, "old-installer", "setup.msi"), 300, old)
	fresh := put(t, filepath.Join(temp, "running.tmp"), 50, time.Hour)
	venv := put(t, filepath.Join(temp, "proj", ".venv", "lib.py"), 20, old)
	put(t, filepath.Join(temp, "proj", "build.log"), 30, old)
	put(t, f.p("AppData", "Local", "CrashDumps", "app.exe.1234.dmp"), 400, old)
	dl := put(t, f.p("Downloads", "old-video.mp4"), 900, old)
	put(t, f.p("Downloads", "new.pdf"), 10, time.Hour)
	// what no category may touch
	keep := []string{
		put(t, f.p("AppData", "Roaming", "Microsoft", "Credentials", "x"), 5, old),
		put(t, f.p("AppData", "Local", "Microsoft", "Windows", "INetCache", "y"), 5, old),
		put(t, f.p("Documents", "thesis.docx"), 5, old),
	}
	ageDirs(t, f.p("AppData"))
	ageDirs(t, f.p("Downloads"))

	cats, st, err := f.sc.ScanWithState(context.Background(), progress.Reporter(nil))
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	var ids []string
	for _, c := range cats {
		sizes[c.ID] = c.Size
		ids = append(ids, c.ID)
	}
	if sizes["win-temp"] != 330 || sizes["win-crashdumps"] != 400 || sizes["old-downloads"] != 900 {
		t.Fatalf("sizes = %v", sizes)
	}

	f.sc.Downloads = st.Downloads
	res, err := f.sc.Clean(context.Background(), ids, progress.Reporter(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Freed != 330+400+900 || !slices.Equal(f.trashed, []string{dl}) {
		t.Fatalf("res = %+v, trashed = %v", res, f.trashed)
	}
	if !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, trash.BinName) }) {
		t.Errorf("notes = %v", res.Notes)
	}
	for _, p := range append(keep, fresh, venv) {
		if !exists(p) {
			t.Errorf("%s was removed", p)
		}
	}
}
