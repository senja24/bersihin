package pathsafe

import (
	"path/filepath"
	"testing"
)

func TestWindowsShapeOK(t *testing.T) {
	for p, want := range map[string]bool{
		`C:\Users\ika\Videos\big.mkv`:           true,
		`d:/Games/old.iso`:                      true,
		`C:\Users\ika\.\x`:                      true,
		`C:\Users\ika\report~final.docx`:        true,
		`C:\Users\ika\Temp~1234567.log`:         true,
		`C:\Users\ika\con.txt.bak\x`:            false,
		`C:\Users\ika\NUL`:                      false,
		`C:\Users\ika\com1.log`:                 false,
		`C:\Users\ika\LPT¹`:                     false,
		`C:\Users\ika\console`:                  true,
		`C:\Users\ika\DOCUME~1`:                 false,
		`C:\PROGRA~2\x`:                         false,
		`C:\Users\ika\Documents.`:               false,
		`C:\Users\ika\Documents \x`:             false,
		`C:\Users\ika\a.txt:secret`:             false,
		`C:\Users\ika\a.txt::$DATA`:             false,
		`C:\Users\ika\..\bob`:                   false,
		`C:/Users/ika/../bob`:                   false,
		`C:\Users\ika\*.iso`:                    false,
		`C:\Users\ika\a?b`:                      false,
		"C:\\Users\\ika\\a\x01b":                false,
		`\\server\share\x`:                      false,
		`\\?\C:\Users\ika\x`:                    false,
		`\\.\C:\x`:                              false,
		`\??\C:\x`:                              false,
		`C:Users\ika`:                           false,
		`1:\x`:                                  false,
		`/Users/ika/x`:                          false,
		``:                                      false,
		`C:`:                                    false,
		`C:\Users\ika\OneDrive - Contoso\a.pdf`: true,
	} {
		if got := WindowsShapeOK(p); got != want {
			t.Errorf("WindowsShapeOK(%q) = %v, want %v", p, got, want)
		}
	}
}

// osPath spells "/C/Users/ika" in this OS's syntax: as it is on Unix,
// "C:\Users\ika" on Windows (and "/D" as "D:\").
func osPath(p string) string {
	if filepath.Separator != '\\' {
		return p
	}
	if len(p) == 2 {
		return p[1:2] + `:\`
	}
	return p[1:2] + ":" + filepath.FromSlash(p[2:])
}

// The Windows rules, spelled in this OS's path syntax: the rules are data,
// only the separator and drive letters are the OS's.
func winGuard() (Guard, string) {
	h := osPath("/C/Users/ika")
	env := map[string]string{
		"SystemDrive":  osPath("/C"),
		"SystemRoot":   osPath("/C/Windows"),
		"ProgramFiles": osPath("/C/Program Files"),
		"ProgramData":  osPath("/C/ProgramData"),
		"APPDATA":      osPath("/E/Profile/Roaming"),
		"LOCALAPPDATA": filepath.Join(h, "AppData", "Local"),
		"OneDrive":     osPath("/D/OneDrive"),
		"PUBLIC":       "relative-ignored",
	}
	g := NewWindowsGuard(h, []string{osPath("/D"), osPath("/E")}, []string{osPath("/D/Docs")}, func(k string) string { return env[k] })
	return g, h
}

func TestWindowsGuardRules(t *testing.T) {
	g, h := winGuard()
	j := func(p string) string { return filepath.Join(h, filepath.FromSlash(p)) }
	d := osPath
	for p, want := range map[string]bool{
		// the user's own files
		j("Videos/holiday.mkv"):          true,
		j("Downloads/setup.exe"):         true,
		j("Projects/app/node_modules"):   true,
		j("Documents/old/archive.zip"):   true,
		d("/D/Games/old.iso"):            true,
		d("/E/Backups/2019.zip"):         true,
		j("Projects/OneDriveExport.zip"): true,
		// the profile's own folders and files
		j("Documents"): false, j("DOWNLOADS"): false, j("Videos"): false, j("Saved Games"): false,
		j("3D Objects"): false, j("Favorites"): false, j("NTUSER.DAT"): false, j("ntuser.dat.LOG1"): false,
		j("UsrClass.dat"): false,
		// AppData and the legacy profile links, whole
		j("AppData"): false, j("AppData/Local/Temp/x.tmp"): false, j("appdata/roaming/Code/x"): false,
		j("Application Data/x"): false, j("Start Menu/Programs/x.lnk"): false,
		j("AppData/Roaming/Microsoft/Credentials/AB12"): false, j("AppData/Local/Microsoft/Edge/User Data"): false,
		// credentials at the top and at any depth
		j(".ssh/id_ed25519"): false, j("Work/repo/.ssh/key"): false, j(".aws/credentials"): false,
		j(".azure/x"): false, j("_netrc"): false, j(".git-credentials"): false, j(".cargo/credentials.toml"): false,
		j(".docker/config.json"): false, j(".kube/config"): false, j(".gnupg/x"): false,
		// cloud sync folders at any depth, and OneDrive where the environment says
		j("OneDrive/Pictures/a.jpg"): false, j("OneDrive - Contoso/Shared/a.pdf"): false, j("Work/Dropbox/a"): false,
		d("/D/OneDrive/x"): false, d("/D/OneDrive"): false, d("/D/Sync/OneDrive/x"): false,
		// a known folder moved to another drive, and a folder holding it
		d("/D/Docs"): false, d("/D"): false,
		// AppData relocated by the environment
		d("/E/Profile/Roaming/app/x"): false, d("/E/Profile"): false,
		// OS folders of a data drive
		d("/D/Program Files/app/x.dll"): false, d("/D/program files (x86)/x"): false, d("/D/$Recycle.Bin/S-1-5/x"): false,
		d("/D/System Volume Information/x"): false, d("/D/Users/old/x"): false, d("/D/Windows/x"): false,
		d("/D/pagefile.sys"): false, d("/D/$WinREAgent/x"): false, d("/D/.hidden/x"): false,
		d("/D/old-pc/AppData/Roaming/x"): false,
		// the system drive outside the profile
		d("/C/Windows/Temp/x"): false, d("/C/ProgramData/x"): false, d("/C/Program Files/x"): false,
		d("/C/Users/Public/x"): false, d("/C/Users/bob/x"): false,
	} {
		if got := g.Safe(p); got != want {
			t.Errorf("Safe(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWindowsGuardIsCaseInsensitive(t *testing.T) {
	g, h := winGuard()
	if !g.Same(filepath.Join(h, "AppData"), filepath.Join(h, "APPDATA")) || !g.FoldCase || !g.Windows {
		t.Fatal("the Windows guard must fold case")
	}
	up := osPath("/c/USERS/IKA/Videos/a.mkv")
	if !g.Safe(up) {
		t.Errorf("%s is the profile in another case", up)
	}
	if g.Safe(osPath("/c/USERS/IKA/.SSH/x")) || g.Safe(osPath("/d/onedrive/x")) {
		t.Error("protected folders must match in any case")
	}
}

func TestWindowsRulesLeaveMacRulesAlone(t *testing.T) {
	g := NewGuard(home, nil)
	if g.Windows || g.Protected != nil || g.rules() != &macRules {
		t.Fatal("NewGuard must keep the macOS rules")
	}
	if wg, _ := winGuard(); wg.rules() != &winRules {
		t.Fatal("NewWindowsGuard must use the Windows rules")
	}
	if filepath.Separator == '\\' {
		t.Skip("macOS paths")
	}
	// AppData means nothing on a Mac, Library does
	if !g.Safe(home+"/AppData/x") || g.Safe(home+"/Library/x") {
		t.Error("macOS rules changed")
	}
}

func TestWindowsKnownFolders(t *testing.T) {
	got := WindowsKnownFolders(osPath("/C/Users/ika"))
	if len(got) != 6 || filepath.Base(got[4]) != "Videos" {
		t.Fatalf("WindowsKnownFolders = %v", got)
	}
}
