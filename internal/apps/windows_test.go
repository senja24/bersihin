package apps

import (
	"context"
	"strings"
	"testing"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/winreg"
)

const (
	hklmUn = `Software\Microsoft\Windows\CurrentVersion\Uninstall`
	wowUn  = `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`
)

func str(s string) winreg.Value { return winreg.Value{Type: winreg.TypeString, Str: s} }
func dw(n uint64) winreg.Value  { return winreg.Value{Type: winreg.TypeDWord, Num: n} }

func program(f *winreg.Fake, root winreg.Root, key string, vals map[string]winreg.Value) {
	f.Key(root, key)
	for k, v := range vals {
		f.Set(root, key, k, v)
	}
}

type launched struct{ exe, args string }

func winFixture(t *testing.T) (*Manager, *winreg.Fake, *[]launched) {
	t.Helper()
	f := &winreg.Fake{}
	program(f, winreg.LocalMachine, hklmUn+`\VLC media player`, map[string]winreg.Value{
		"DisplayName": str("VLC media player"), "DisplayVersion": str("3.0.21"), "Publisher": str("VideoLAN"),
		"UninstallString": str(`"C:\Program Files\VideoLAN\VLC\uninstall.exe"`), "EstimatedSize": dw(170000),
		"DisplayIcon": str(`C:\Program Files\VideoLAN\VLC\vlc.exe,0`), "InstallDate": str("20250312"),
		"InstallLocation": str(`C:\Program Files\VideoLAN\VLC`),
	})
	program(f, winreg.LocalMachine, wowUn+`\{11111111-2222-3333-4444-555555555555}`, map[string]winreg.Value{
		"DisplayName": str("Old Tool"), "Publisher": str("Acme"), "UninstallString": str(`MsiExec.exe /I{11111111-2222-3333-4444-555555555555}`),
	})
	program(f, winreg.CurrentUser, hklmUn+`\Spotify`, map[string]winreg.Value{
		"DisplayName": str("Spotify"), "UninstallString": str(`C:\Users\ika\AppData\Roaming\Spotify\Spotify.exe /uninstall`),
	})
	program(f, winreg.LocalMachine, hklmUn+`\VCRedist`, map[string]winreg.Value{
		"DisplayName": str("Microsoft Visual C++ 2022 X64 Minimum Runtime"), "Publisher": str("Microsoft Corporation"),
		"UninstallString": str(`MsiExec.exe /X{AAAAAAAA-2222-3333-4444-555555555555}`),
	})
	program(f, winreg.LocalMachine, hklmUn+`\Locked`, map[string]winreg.Value{
		"DisplayName": str("Locked App"), "NoRemove": dw(1), "UninstallString": str(`C:\x\u.exe`),
	})
	program(f, winreg.LocalMachine, hklmUn+`\NoCmd`, map[string]winreg.Value{"DisplayName": str("No Command")})
	program(f, winreg.LocalMachine, hklmUn+`\Quiet`, map[string]winreg.Value{
		"DisplayName": str("Quiet Only"), "QuietUninstallString": str(`"C:\Q\unins000.exe" /SILENT`),
	})
	// hidden as Settings hides them
	program(f, winreg.LocalMachine, hklmUn+`\Component`, map[string]winreg.Value{"DisplayName": str("A Component"), "SystemComponent": dw(1), "UninstallString": str(`C:\c.exe`)})
	program(f, winreg.LocalMachine, hklmUn+`\KB123`, map[string]winreg.Value{"DisplayName": str("Update for X"), "ParentKeyName": str("X"), "UninstallString": str(`C:\c.exe`)})
	program(f, winreg.LocalMachine, hklmUn+`\Hotfix`, map[string]winreg.Value{"DisplayName": str("Hotfix 1"), "ReleaseType": str("Hotfix")})
	program(f, winreg.LocalMachine, hklmUn+`\Nameless`, map[string]winreg.Value{"UninstallString": str(`C:\c.exe`)})
	// the same program in both views is listed once
	program(f, winreg.LocalMachine, wowUn+`\VLC media player`, map[string]winreg.Value{"DisplayName": str("VLC media player"), "DisplayVersion": str("3.0.21")})

	var calls []launched
	env := map[string]string{"SystemRoot": `C:\WINDOWS`}
	m := &Manager{Registry: f, Getenv: func(k string) string { return env[k] }, Launch: func(exe, args string) error {
		calls = append(calls, launched{exe, args})
		return nil
	}}
	return m, f, &calls
}

func winByName(apps []model.InstalledApp) map[string]model.InstalledApp {
	out := map[string]model.InstalledApp{}
	for _, a := range apps {
		out[a.Name] = a
	}
	return out
}

func TestWindowsListPrograms(t *testing.T) {
	m, _, _ := winFixture(t)
	apps, err := m.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := winByName(apps)
	var names []string
	for _, a := range apps {
		names = append(names, a.Name)
	}
	want := "Locked App,Microsoft Visual C++ 2022 X64 Minimum Runtime,No Command,Old Tool,Quiet Only,Spotify,VLC media player"
	if strings.Join(names, ",") != want {
		t.Fatalf("listed %v", names)
	}
	vlc := got["VLC media player"]
	if vlc.Size != 170000*1024 || vlc.Version != "3.0.21" || vlc.Publisher != "VideoLAN" || vlc.IconPath != `C:\Program Files\VideoLAN\VLC\vlc.exe` ||
		vlc.Installed == 0 || vlc.Path != `C:\Program Files\VideoLAN\VLC` || !vlc.CanUninstall || !strings.HasPrefix(vlc.ID, "win:") {
		t.Fatalf("vlc = %+v", vlc)
	}
	for name, can := range map[string]bool{
		"Old Tool": true, "Spotify": true, "Quiet Only": true,
		"Microsoft Visual C++ 2022 X64 Minimum Runtime": false, "Locked App": false, "No Command": false,
	} {
		if got[name].CanUninstall != can {
			t.Errorf("%s: canUninstall = %v, want %v", name, got[name].CanUninstall, can)
		}
	}
}

func TestWindowsUninstallLaunches(t *testing.T) {
	m, _, calls := winFixture(t)
	apps, _ := m.List(context.Background(), nil)
	got := winByName(apps)

	res := m.Uninstall(context.Background(), got["Old Tool"].ID)
	if !res.OK || !res.Pending {
		t.Fatalf("Old Tool: %+v", res)
	}
	res = m.Uninstall(context.Background(), got["VLC media player"].ID)
	if !res.OK {
		t.Fatalf("VLC: %+v", res)
	}
	want := []launched{
		{`C:\WINDOWS\System32\msiexec.exe`, "/X{11111111-2222-3333-4444-555555555555}"},
		{`C:\Program Files\VideoLAN\VLC\uninstall.exe`, ""},
	}
	if len(*calls) != 2 || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("launched %+v", *calls)
	}
	// locked and unknown ids never launch
	for _, id := range []string{got["Locked App"].ID, got["Microsoft Visual C++ 2022 X64 Minimum Runtime"].ID, "win:nope", ""} {
		if res := m.Uninstall(context.Background(), id); res.OK {
			t.Fatalf("%q: %+v", id, res)
		}
	}
	if len(*calls) != 2 {
		t.Fatalf("launched %+v", *calls)
	}
}

func TestWindowsUninstallRereadsKey(t *testing.T) {
	m, f, calls := winFixture(t)
	apps, _ := m.List(context.Background(), nil)
	id := winByName(apps)["Spotify"].ID
	// the key now names another program: refused
	f.Set(winreg.CurrentUser, hklmUn+`\Spotify`, "DisplayName", str("Something Else"))
	if res := m.Uninstall(context.Background(), id); res.OK {
		t.Fatalf("renamed: %+v", res)
	}
	// its command became one that may not run: refused
	f.Set(winreg.CurrentUser, hklmUn+`\Spotify`, "DisplayName", str("Spotify"))
	f.Set(winreg.CurrentUser, hklmUn+`\Spotify`, "UninstallString", str(`\\server\share\evil.exe`))
	if res := m.Uninstall(context.Background(), id); res.OK {
		t.Fatalf("UNC command: %+v", res)
	}
	// gone
	f.Set(winreg.CurrentUser, hklmUn+`\Spotify`, "DisplayName", str(""))
	if res := m.Uninstall(context.Background(), id); res.OK || !strings.Contains(res.Message, "gone") {
		t.Fatalf("gone: %+v", res)
	}
	if len(*calls) != 0 {
		t.Fatalf("launched %+v", *calls)
	}
}

func TestWindowsUninstallLaunchFails(t *testing.T) {
	m, _, _ := winFixture(t)
	m.Launch = func(string, string) error { return context.Canceled }
	apps, _ := m.List(context.Background(), nil)
	if res := m.Uninstall(context.Background(), winByName(apps)["Spotify"].ID); res.OK || !strings.Contains(res.Message, "Settings") {
		t.Fatalf("%+v", res)
	}
}

func TestWindowsNoLauncherNothingUninstallable(t *testing.T) {
	m, _, _ := winFixture(t)
	m.Launch = nil
	apps, _ := m.List(context.Background(), nil)
	for _, a := range apps {
		if a.CanUninstall {
			t.Fatalf("%s offered without a launcher", a.Name)
		}
	}
}

func TestWindowsUninstallCommand(t *testing.T) {
	env := func(k string) string {
		return map[string]string{"SystemRoot": `C:\Windows`, "ProgramFiles": `C:\Program Files`}[k]
	}
	cases := []struct{ in, exe, args string }{
		{`"C:\Program Files\App\unins000.exe"`, `C:\Program Files\App\unins000.exe`, ""},
		{`"C:\Program Files\App\unins000.exe" /SILENT`, `C:\Program Files\App\unins000.exe`, "/SILENT"},
		{`C:\Program Files\App\uninstall.exe /x`, `C:\Program Files\App\uninstall.exe`, "/x"},
		{`C:\Program Files\My.exed\u.exe --remove`, `C:\Program Files\My.exed\u.exe`, "--remove"},
		{`%ProgramFiles%\App\u.exe`, `C:\Program Files\App\u.exe`, ""},
		{`MsiExec.exe /I{12345678-ABCD-ABCD-ABCD-123456789012}`, `C:\Windows\System32\msiexec.exe`, "/X{12345678-ABCD-ABCD-ABCD-123456789012}"},
		{`msiexec /x {12345678-abcd-abcd-abcd-123456789012} /qn`, `C:\Windows\System32\msiexec.exe`, "/X{12345678-ABCD-ABCD-ABCD-123456789012}"},
	}
	for _, c := range cases {
		exe, args, ok := UninstallCommand(c.in, env)
		if !ok || exe != c.exe || args != c.args {
			t.Errorf("%q -> %q %q %v", c.in, exe, args, ok)
		}
	}
	for _, bad := range []string{
		"", `uninstall.exe`, `\\server\share\u.exe`, `"C:\App\u.exe`, `C:\App\script.bat`, `msiexec /package foo.msi`,
		`MsiExec.exe /I{not-a-guid}`, "C:\\a.exe\r\nC:\\b.exe", `rundll32 x`, `C:\App\u*.exe`, `%UNKNOWN%\u.exe`,
	} {
		if exe, args, ok := UninstallCommand(bad, env); ok {
			t.Errorf("%q accepted as %q %q", bad, exe, args)
		}
	}
}

func TestWindowsIconFile(t *testing.T) {
	for in, want := range map[string]string{
		`C:\App\app.exe,0`:      `C:\App\app.exe`,
		`"C:\App\app.exe",-101`: `C:\App\app.exe`,
		`C:\App\app.ico`:        `C:\App\app.ico`,
		`C:\App,v2\app.exe`:     `C:\App,v2\app.exe`,
		`C:\App\app.dll,3`:      "",
		`\\server\x.exe`:        "",
		`%SystemRoot%\x.exe`:    "",
		``:                      "",
	} {
		if got := iconFile(in); got != want {
			t.Errorf("iconFile(%q) = %q, want %q", in, got, want)
		}
	}
}
