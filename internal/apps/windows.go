package apps

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/winreg"
)

// The Windows side of the Manager: the programs Settings > Apps lists, read
// from the Uninstall keys of the registry. It is platform-neutral, so its rules
// run and are tested on any OS against winreg.Fake; apps_windows.go plugs in
// the real registry and the launcher.

// uninstallKey is one place programs register themselves.
type uninstallKey struct {
	root winreg.Root
	path string
}

// uninstallKeys are read in this order: the 64-bit view, the 32-bit view
// (WOW6432Node) and the per-user programs.
var uninstallKeys = []uninstallKey{
	{winreg.LocalMachine, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
	{winreg.LocalMachine, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{winreg.CurrentUser, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// winTarget is what Uninstall needs of a listed program: where its key is
// and what it was called, so a key changed since the list is not acted on.
type winTarget struct {
	root winreg.Root
	key  string // full path of the program's own key
	name string
}

// winProgram is one entry of an Uninstall key.
type winProgram struct {
	winTarget
	version, publisher, location, icon string
	size, installed                    int64
	command                            string // UninstallString
	locked                             bool
}

// updateRelease are the ReleaseType values of updates, which Settings does not list as programs.
var updateRelease = map[string]bool{"security update": true, "update rollup": true, "hotfix": true, "update": true, "service pack": true}

// windowsParts are Microsoft's runtimes and parts of Windows: other programs
// need them, so they are shown but never offered for removal.
var windowsParts = regexp.MustCompile(`(?i)^(microsoft (visual c\+\+|\.net|asp\.net|windows desktop runtime|edge|update health|gameinput|vs ?code redistributable)|windows |\.net |microsoft edge webview2|microsoft (onedrive|teams meeting add-in))`)

// readProgram reads one program's key; ok is false for an entry Settings
// hides too: no name, a system component, an update or a part of another program.
func readProgram(reg winreg.Registry, root winreg.Root, key string) (winProgram, bool) {
	v, err := reg.Values(root, key)
	if err != nil {
		return winProgram{}, false
	}
	p := winProgram{winTarget: winTarget{root: root, key: key, name: strings.TrimSpace(winreg.Str(v, "DisplayName"))}}
	if p.name == "" || len(p.name) > 300 {
		return p, false
	}
	if n, _ := winreg.Num(v, "SystemComponent"); n == 1 {
		return p, false
	}
	if winreg.Str(v, "ParentKeyName") != "" || updateRelease[strings.ToLower(winreg.Str(v, "ReleaseType"))] {
		return p, false
	}
	p.version = winreg.Str(v, "DisplayVersion")
	p.publisher = strings.TrimSpace(winreg.Str(v, "Publisher"))
	p.location = strings.Trim(winreg.Str(v, "InstallLocation"), `" `)
	p.icon = iconFile(winreg.Str(v, "DisplayIcon"))
	if kb, ok := winreg.Num(v, "EstimatedSize"); ok && kb < 1<<40 {
		p.size = int64(kb) * 1024
	}
	p.installed = installDate(winreg.Str(v, "InstallDate"))
	// only UninstallString, as Settings: QuietUninstallString would remove the
	// program with no window of its own, so an entry with only that is locked
	p.command = winreg.Str(v, "UninstallString")
	noRemove, _ := winreg.Num(v, "NoRemove")
	p.locked = noRemove == 1 || windowsParts.MatchString(p.name)
	return p, true
}

// installDate reads InstallDate, written as YYYYMMDD; 0 when it is not one.
func installDate(s string) int64 {
	t, err := time.Parse("20060102", strings.TrimSpace(s))
	if err != nil || t.Year() < 1990 {
		return 0
	}
	return t.UnixMilli()
}

// iconFile is the file of DisplayIcon ("C:\x\app.exe,0" → C:\x\app.exe), or
// "" unless it is a drive-letter path to an .exe or .ico.
func iconFile(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, ','); i > 0 && !strings.ContainsAny(s[i:], `\/`) {
		s = s[:i]
	}
	s = strings.Trim(s, `" `)
	low := strings.ToLower(s)
	if !winAbs(s) || !(strings.HasSuffix(low, ".exe") || strings.HasSuffix(low, ".ico")) {
		return ""
	}
	return s
}

// winAbs reports whether s is a plain drive-letter path, C:\...
func winAbs(s string) bool {
	return len(s) > 3 && (s[0]|0x20) >= 'a' && (s[0]|0x20) <= 'z' && s[1] == ':' && s[2] == '\\' && !strings.ContainsAny(s, "\x00\r\n")
}

var (
	msiGUID  = regexp.MustCompile(`(?i)^/[IX]\s*(\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\})`)
	envToken = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_()]*)%`)
)

// expandEnv expands %NAME% as REG_EXPAND_SZ values do; unknown names stay as written.
func expandEnv(s string, env func(string) string) string {
	return envToken.ReplaceAllStringFunc(s, func(t string) string {
		if v := env(t[1 : len(t)-1]); v != "" {
			return v
		}
		return t
	})
}

// UninstallCommand splits an UninstallString into the program to start and
// its arguments, as Settings would run it. The program must be a drive-letter
// path to an .exe; Windows Installer entries ("MsiExec.exe /I{GUID}", which
// opens the repair dialog) become "msiexec.exe /X{GUID}", the removal, with no
// other arguments. ok is false for anything else.
func UninstallCommand(s string, env func(string) string) (exe, args string, ok bool) {
	s = strings.TrimSpace(expandEnv(s, env))
	if s == "" || len(s) > 2000 || strings.ContainsAny(s, "\x00\r\n") {
		return "", "", false
	}
	if rest, found := strings.CutPrefix(s, `"`); found {
		i := strings.IndexByte(rest, '"')
		if i < 0 {
			return "", "", false
		}
		exe, args = rest[:i], strings.TrimSpace(rest[i+1:])
	} else if low := strings.ToLower(s); strings.HasPrefix(low, "msiexec ") {
		exe, args = s[:7], strings.TrimSpace(s[7:])
	} else {
		// unquoted: the path runs up to the first ".exe" that ends a word,
		// as CreateProcess reads C:\Program Files\App\unins000.exe /x
		low := strings.ToLower(s)
		end := -1
		for i := 0; ; {
			j := strings.Index(low[i:], ".exe")
			if j < 0 {
				break
			}
			k := i + j + 4
			if k == len(s) || s[k] == ' ' || s[k] == '\t' {
				end = k
				break
			}
			i = k
		}
		if end < 0 {
			return "", "", false
		}
		exe, args = s[:end], strings.TrimSpace(s[end:])
	}
	base := strings.ToLower(exe[strings.LastIndexAny(exe, `\/`)+1:])
	if base == "msiexec" || base == "msiexec.exe" {
		m := msiGUID.FindStringSubmatch(args)
		if m == nil {
			return "", "", false
		}
		sysRoot := env("SystemRoot")
		if !winAbs(sysRoot + `\`) {
			sysRoot = `C:\Windows`
		}
		return strings.TrimRight(sysRoot, `\`) + `\System32\msiexec.exe`, "/X" + strings.ToUpper(m[1]), true
	}
	if !winAbs(exe) || !strings.HasSuffix(strings.ToLower(exe), ".exe") || strings.ContainsAny(exe, `"*?<>|`) {
		return "", "", false
	}
	return exe, args, true
}

func (m *Manager) env(name string) string {
	if m.Getenv == nil {
		return ""
	}
	return m.Getenv(name)
}

// programs reads every Uninstall key. A program registered twice (both
// registry views, or per user and for all users) is listed once.
func (m *Manager) programs() []winProgram {
	var out []winProgram
	seen := map[string]bool{}
	for _, u := range uninstallKeys {
		names, err := m.Registry.SubKeys(u.root, u.path)
		if err != nil {
			continue
		}
		for _, n := range names {
			p, ok := readProgram(m.Registry, u.root, u.path+`\`+n)
			if !ok {
				continue
			}
			k := strings.ToLower(p.name + "\x00" + p.version)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

// listWindows is List on Windows.
func (m *Manager) listWindows(ctx context.Context, rep progress.Reporter) ([]model.InstalledApp, error) {
	rep.Call("Finding apps", 0, 1, "")
	progs := m.programs()
	if ctx.Err() != nil {
		return nil, progress.ErrCancelled
	}
	apps := []model.InstalledApp{}
	table := map[string]winTarget{}
	for _, p := range progs {
		_, _, runnable := UninstallCommand(p.command, m.env)
		id := "win:" + shortHash(p.root.String()+`\`+strings.ToLower(p.key))
		can := runnable && !p.locked && m.Launch != nil
		apps = append(apps, model.InstalledApp{
			ID: id, Name: p.name, Version: p.version, Publisher: p.publisher, Size: p.size, Installed: p.installed,
			Path: p.location, IconPath: p.icon, CanUninstall: can,
		})
		if can {
			table[id] = p.winTarget
		}
	}
	slices.SortStableFunc(apps, func(a, b model.InstalledApp) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	m.mu.Lock()
	m.win = table
	m.mu.Unlock()
	rep.Call("Done", 1, 1, "")
	return apps, nil
}

// uninstallWindows starts the program's own uninstaller. The page has the
// user confirm first (and the API redeems that confirmation); the key is read
// again here, so a key that changed since the list, or now names something
// that may not be run, is refused. Windows asks for permission (UAC) before an
// uninstaller that needs it runs: Bersihin never elevates by itself.
func (m *Manager) uninstallWindows(ctx context.Context, id string) model.UninstallResult {
	no := model.UninstallResult{Message: "This app cannot be removed from Bersihin"}
	if m.Launch == nil {
		return model.UninstallResult{Message: "Uninstalling is not supported on this system"}
	}
	m.mu.Lock()
	empty := m.win == nil
	m.mu.Unlock()
	if empty {
		if _, err := m.listWindows(ctx, nil); err != nil {
			return no
		}
	}
	m.mu.Lock()
	t, ok := m.win[id]
	m.mu.Unlock()
	if !ok {
		return no
	}
	p, ok := readProgram(m.Registry, t.root, t.key)
	if !ok && p.name == "" {
		return model.UninstallResult{Message: "The app is already gone"}
	}
	if !ok || p.name != t.name || p.locked {
		return no
	}
	exe, args, ok := UninstallCommand(p.command, m.env)
	if !ok {
		return no
	}
	if err := m.Launch(exe, args); err != nil {
		return model.UninstallResult{Message: "Windows did not start the uninstaller of " + p.name + ". Remove it in Settings > Apps"}
	}
	return model.UninstallResult{OK: true, Pending: true, Message: "The uninstaller of " + p.name + " has opened. Follow its steps, then look again"}
}
