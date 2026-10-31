package pathsafe

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// The Windows rules are data like the macOS ones, so they are tested on any
// OS; WindowsShapeOK reads Windows path syntax itself, whatever the OS.

// winRules are the protected names of a Windows profile (C:\Users\name).
var winRules = rules{
	protectedTop: []string{
		"Desktop", "Documents", "Downloads", "Pictures", "Videos", "Music", "Favorites", "Contacts", "Links",
		"Saved Games", "Searches", "3D Objects", "OneDrive", "iCloudDrive", "iCloud Drive", "iCloud Photos",
		"Dropbox", "Google Drive", "Creative Cloud Files", ".config", ".local",
	},
	// AppData holds every app's settings, credentials (Microsoft\Credentials,
	// Protect, Vault) and browser profiles; the others are the links a
	// profile keeps for old programs.
	denyTop: []string{
		"AppData", "Application Data", "Local Settings", "Cookies", "NetHood", "PrintHood", "Recent",
		"SendTo", "Start Menu", "Templates", "My Documents",
	},
	// the registry hive of the profile: ntuser.dat, ntuser.dat.LOG1, UsrClass.dat…
	topPrefixes: []string{"ntuser", "usrclass"},
	secretDirs: [][]string{
		{".ssh"}, {".gnupg"}, {".aws"}, {".azure"}, {".kube"}, {".docker"}, {".netrc"}, {"_netrc"},
		{".password-store"}, {".npmrc"}, {".yarnrc"}, {".yarnrc.yml"}, {".pgpass"}, {".pypirc"}, {".git-credentials"},
		{".terraform.d"}, {".config"}, {".claude"}, {".codex"}, {".cloudflared"}, {".mcp-auth"}, {".ollama"},
		{".gem", "credentials"}, {".m2", "settings.xml"}, {".gradle", "gradle.properties"},
		{".vault-token"}, {".boto"}, {".s3cfg"},
		{"AppData", "Roaming", "Microsoft"}, {"AppData", "Local", "Microsoft"}, {"AppData", "LocalLow", "Microsoft"},
		{"AppData", "Roaming", "gnupg"}, {"AppData", "Roaming", "1Password"}, {"AppData", "Local", "1Password"},
		{"AppData", "Roaming", "Bitwarden"}, {"AppData", "Roaming", "KeePass"}, {"AppData", "Roaming", "Mozilla"},
		{"AppData", "Roaming", "Thunderbird"}, {"AppData", "Roaming", "GitHub CLI"}, {"AppData", "Roaming", "gcloud"},
	},
	secretGlobs: [][2]string{{".cargo", "credentials"}},
	cloudDirs:   [][]string{{"OneDrive"}, {"iCloudDrive"}, {"iCloud Photos"}},
	driveProtectedTop: []string{
		"Windows", "Program Files", "Program Files (x86)", "ProgramData", "Users", "Documents and Settings",
		"System Volume Information", "Recovery", "PerfLogs", "Boot", "EFI", "bootmgr", "BOOTNXT",
		"pagefile.sys", "hiberfil.sys", "swapfile.sys", "DumpStack.log", "DumpStack.log.tmp",
		"Config.Msi", "MSOCache", "WindowsApps", "WpSystem", "Intel", "OneDriveTemp", "ESD", "RECYCLER", "RECYCLED",
	},
	// $Recycle.Bin, $WinREAgent, $Windows.~BT, $SysReset…
	drivePrefixes: []string{"$"},
}

// WindowsKnownFolders are the usual user folders of a Windows profile.
func WindowsKnownFolders(home string) []string {
	var out []string
	for _, n := range []string{"Desktop", "Documents", "Downloads", "Pictures", "Videos", "Music"} {
		out = append(out, filepath.Join(home, n))
	}
	return out
}

// windowsSystemFolders lists the OS folders, from the environment and, for
// a variable that is missing, at their usual place on the system drive.
func windowsSystemFolders(env func(string) string) []string {
	drive := env("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	root := drive + string(filepath.Separator)
	out := []string{
		filepath.Join(root, "Windows"), filepath.Join(root, "Program Files"), filepath.Join(root, "Program Files (x86)"),
		filepath.Join(root, "ProgramData"), filepath.Join(root, "Users", "Public"), filepath.Join(root, "Users", "Default"),
		filepath.Join(root, "$Recycle.Bin"), filepath.Join(root, "System Volume Information"), filepath.Join(root, "Recovery"),
	}
	for _, v := range []string{
		"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData", "ALLUSERSPROFILE",
		"CommonProgramFiles", "CommonProgramFiles(x86)", "CommonProgramW6432", "PUBLIC",
	} {
		out = append(out, env(v))
	}
	return absOnly(out)
}

// windowsProtected lists the folders protected wherever they are: the OS
// folders, AppData (and its Microsoft folders, which hold the credential
// and key stores) where the environment puts it, and every OneDrive folder.
func windowsProtected(env func(string) string) []string {
	out := windowsSystemFolders(env)
	for _, v := range []string{"APPDATA", "LOCALAPPDATA"} {
		if p := env(v); p != "" {
			out = append(out, p, filepath.Join(p, "Microsoft"))
		}
	}
	for _, v := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		out = append(out, env(v))
	}
	return absOnly(out)
}

func absOnly(list []string) []string {
	var out []string
	for _, p := range list {
		if p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// NewWindowsGuard returns the Windows rules for the profile folder home,
// the other fixed drives, the known folders where the shell says they are
// (Documents moved to D:\Docs) and the environment (for the system, AppData
// and OneDrive folders). Names compare case-insensitively.
func NewWindowsGuard(home string, drives, known []string, env func(string) string) Guard {
	return Guard{
		Home:        home,
		Known:       append(WindowsKnownFolders(home), absOnly(known)...),
		Drives:      drives,
		SystemRoots: windowsSystemFolders(env),
		Protected:   windowsProtected(env),
		FoldCase:    true,
		Windows:     true,
	}
}

// ForPlatform returns the rules of the running OS: NewWindowsGuard on
// Windows, NewGuard (the macOS rules) elsewhere.
func ForPlatform(home string, drives []string) Guard {
	if runtime.GOOS == "windows" {
		return NewWindowsGuard(home, drives, shellKnownFolders(), os.Getenv)
	}
	return NewGuard(home, drives)
}

var (
	// an 8.3 short name ("DOCUME~1", "PROGRA~2.OLD") can name a protected
	// folder in a spelling no rule lists
	shortName = regexp.MustCompile(`^[^~.\s]{1,6}~[0-9]+(\.[^.]{0,3})?$`)
	// device names Win32 opens whatever the folder and extension
	deviceName = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|CONIN\$|CONOUT\$|COM[0-9¹²³]|LPT[0-9¹²³])$`)
)

// isShortName: at most 8 characters, a "~" and a number, and an extension
// of at most 3.
func isShortName(seg string) bool {
	base, _, _ := strings.Cut(seg, ".")
	return len(base) <= 8 && shortName.MatchString(seg)
}

// WindowsShapeOK reports whether p is a plain drive letter path (C:\x\y) the
// guard can reason about. Refused: UNC and device paths (\\server\share,
// \\?\, \\.\), drive-relative paths (C:x), a ".." step, an alternate data
// stream (a ":" after the drive), a name Win32 would change (a trailing dot
// or space: "Documents." opens Documents), an 8.3 short name, a device
// name (NUL, COM1.txt), wildcards and the other characters Windows does
// not allow in names. Both separators count, as they do on Windows.
func WindowsShapeOK(p string) bool {
	if len(p) < 3 || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return false
	}
	if c := p[0] | 0x20; c < 'a' || c > 'z' {
		return false
	}
	for _, seg := range strings.FieldsFunc(p[3:], func(r rune) bool { return r == '\\' || r == '/' }) {
		if seg == "." {
			continue
		}
		if seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") || isShortName(seg) {
			return false
		}
		if strings.ContainsAny(seg, `:<>"|?*`) {
			return false
		}
		for _, r := range seg {
			if r < 0x20 {
				return false
			}
		}
		base, _, _ := strings.Cut(seg, ".")
		if deviceName.MatchString(strings.TrimRight(base, " ")) {
			return false
		}
	}
	return true
}
