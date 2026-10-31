package startup

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/winreg"
)

func sz(s string) winreg.Value { return winreg.Value{Type: winreg.TypeString, Str: s} }
func bin(b ...byte) winreg.Value {
	return winreg.Value{Type: winreg.TypeBinary, Bin: append(b, make([]byte, 12-len(b))...)}
}

const tasksXML = `<?xml version="1.0" encoding="UTF-8"?>
<Tasks>
<!-- \Microsoft\Windows\Defrag\ScheduledDefrag -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger/></Triggers>
  <Actions><Exec><Command>defrag.exe</Command></Exec></Actions>
</Task>
<!-- \GoogleUpdateTaskMachineUA -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><TimeTrigger/></Triggers>
  <Actions><Exec><Command>C:\Google\Update.exe</Command></Exec></Actions>
</Task>
<!-- \Vendor\Razer Synapse Startup -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers>
  <Settings><Enabled>false</Enabled></Settings>
  <Actions><Exec><Command>"C:\Program Files\Razer\Synapse.exe"</Command><Arguments>--tray</Arguments></Exec></Actions>
</Task>
<!-- \Caf` + "\xe9" + ` Helper -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger/><BootTrigger/></Triggers>
  <Actions><Exec><Command>C:\Cafe\helper.exe</Command></Exec></Actions>
</Task>
<!-- \NoExec -->
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger/></Triggers>
  <Actions><ComHandler><ClassId>{x}</ClassId></ComHandler></Actions>
</Task>
</Tasks>`

type winFix struct {
	m      *Manager
	reg    *winreg.Fake
	user   string
	common string
}

func newWinFixture(t *testing.T) *winFix {
	t.Helper()
	base := t.TempDir()
	f := &winFix{reg: &winreg.Fake{}, user: filepath.Join(base, "Startup"), common: filepath.Join(base, "CommonStartup")}
	os.MkdirAll(f.user, 0o755)
	os.MkdirAll(f.common, 0o755)
	os.WriteFile(filepath.Join(f.user, "Notes.lnk"), nil, 0o644)
	os.WriteFile(filepath.Join(f.user, "desktop.ini"), nil, 0o644)
	os.MkdirAll(filepath.Join(f.user, "sub"), 0o755)
	os.WriteFile(filepath.Join(f.common, "Backup Agent.lnk"), nil, 0o644)

	r := f.reg
	r.Set(winreg.CurrentUser, runKey, "Discord", sz(`"C:\Users\ika\AppData\Local\Discord\Update.exe" --processStart Discord.exe`))
	r.Set(winreg.CurrentUser, runKey, "OneDrive", sz(`"C:\Program Files\Microsoft OneDrive\OneDrive.exe" /background`))
	r.Set(winreg.CurrentUser, runKey, "{2C7EBA1D-1234}", sz(`C:\Tools\clipper.exe -q`))
	r.Set(winreg.CurrentUser, runKey, "Empty", sz(" "))
	r.Set(winreg.CurrentUser, runKey, "Number", winreg.Value{Type: winreg.TypeDWord, Num: 1})
	r.Set(winreg.LocalMachine, runKey, "SecurityHealth", winreg.Value{Type: winreg.TypeExpandString, Str: `%windir%\system32\SecurityHealthSystray.exe`})
	r.Set(winreg.LocalMachine, runKey32, "Steam", sz(`"C:\Program Files (x86)\Steam\steam.exe" -silent`))
	// OneDrive was turned off in Task Manager, Notes too; Steam's HKLM value says off
	r.Set(winreg.CurrentUser, approvedKey+`\Run`, "onedrive", bin(0x03, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8))
	r.Set(winreg.CurrentUser, approvedKey+`\Run`, "Discord", bin(0x02))
	r.Set(winreg.CurrentUser, approvedKey+`\StartupFolder`, "Notes.lnk", bin(0x07))
	r.Set(winreg.LocalMachine, approvedKey+`\Run32`, "Steam", bin(0x03))
	// HKEY_LOCAL_MACHINE is never written
	r.Denied = []string{`HKLM\` + approvedKey + `\Run`, `HKLM\` + approvedKey + `\Run32`, `HKLM\` + approvedKey + `\StartupFolder`}

	f.m = &Manager{
		Registry: r, Now: func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) },
		Win: WinSources{UserFolder: f.user, CommonFolder: f.common, Tasks: func(context.Context) (string, error) { return tasksXML, nil }},
	}
	return f
}

func itemsByID(items []model.StartupItem) map[string]model.StartupItem {
	out := map[string]model.StartupItem{}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func TestWindowsStartupList(t *testing.T) {
	f := newWinFixture(t)
	items, err := f.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := itemsByID(items)
	want := map[string]struct {
		name            string
		enabled, toggle bool
		location        string
	}{
		"win:run:discord":                        {"Discord", true, true, "Run key"},
		"win:run:onedrive":                       {"OneDrive", false, true, "Run key"},
		"win:run:{2c7eba1d-1234}":                {"clipper", true, true, "Run key"},
		"win:run-all:securityhealth":             {"SecurityHealth", true, false, "Run key (all users)"},
		"win:run32-all:steam":                    {"Steam", false, false, "Run key (all users, 32-bit)"},
		"win:folder:notes.lnk":                   {"Notes", false, true, "Startup folder"},
		"win:folder-all:backup agent.lnk":        {"Backup Agent", true, false, "Startup folder (all users)"},
		`win:task:\vendor\razer synapse startup`: {"Razer Synapse Startup", false, false, "Task Scheduler"},
		"win:task:\\caf\ufffd helper":            {"Caf\ufffd Helper", true, false, "Task Scheduler"},
	}
	if len(items) != len(want) {
		var ids []string
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		t.Fatalf("listed %d items: %v", len(items), ids)
	}
	for id, w := range want {
		it, ok := got[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if it.Name != w.name || it.Enabled != w.enabled || it.CanToggle != w.toggle || it.NeedsAdmin == w.toggle || it.Location != w.location {
			t.Errorf("%s = %+v", id, it)
		}
	}
	if got[`win:task:\vendor\razer synapse startup`].Command != `"C:\Program Files\Razer\Synapse.exe" --tray` {
		t.Errorf("task command = %q", got[`win:task:\vendor\razer synapse startup`].Command)
	}
	if items[0].Impact != model.ImpactHigh {
		t.Errorf("heaviest first: %+v", items[0])
	}
}

func TestWindowsStartupToggleUserItems(t *testing.T) {
	f := newWinFixture(t)
	items, err := f.m.Set(context.Background(), "win:run:discord", false)
	if err != nil {
		t.Fatal(err)
	}
	if itemsByID(items)["win:run:discord"].Enabled {
		t.Fatal("Discord still on")
	}
	v, _ := f.reg.Values(winreg.CurrentUser, approvedKey+`\Run`)
	b := v["Discord"].Bin
	if len(b) != 12 || b[0] != 0x03 || binary.LittleEndian.Uint64(b[4:]) != 134379216000000000 {
		t.Fatalf("approved value = % x", b)
	}
	// the Run value itself is untouched
	run, _ := f.reg.Values(winreg.CurrentUser, runKey)
	if !strings.Contains(run["Discord"].Str, "Update.exe") {
		t.Fatal("Run value changed")
	}

	items, err = f.m.Set(context.Background(), "win:folder:notes.lnk", true)
	if err != nil || !itemsByID(items)["win:folder:notes.lnk"].Enabled {
		t.Fatalf("Notes: %v", err)
	}
	v, _ = f.reg.Values(winreg.CurrentUser, approvedKey+`\StartupFolder`)
	if b := v["Notes.lnk"].Bin; len(b) != 12 || b[0] != 0x02 {
		t.Fatalf("Notes approved = % x", b)
	}
	if f.reg.Writes != 2 {
		t.Fatalf("writes = %d", f.reg.Writes)
	}
}

func TestWindowsStartupAllUsersReadOnly(t *testing.T) {
	f := newWinFixture(t)
	f.m.List(context.Background())
	for _, id := range []string{"win:run-all:securityhealth", "win:run32-all:steam", "win:folder-all:backup agent.lnk", `win:task:\vendor\razer synapse startup`, "win:run:nope", "mac:user:x"} {
		if _, err := f.m.Set(context.Background(), id, true); err == nil {
			t.Errorf("%s changed", id)
		}
	}
	if f.reg.Writes != 0 {
		t.Fatalf("writes = %d", f.reg.Writes)
	}
}

func TestWindowsStartupGoneItem(t *testing.T) {
	f := newWinFixture(t)
	f.m.List(context.Background())
	os.Remove(filepath.Join(f.user, "Notes.lnk"))
	if _, err := f.m.Set(context.Background(), "win:folder:notes.lnk", true); err == nil {
		t.Fatal("a removed shortcut was toggled")
	}
	if f.reg.Writes != 0 {
		t.Fatalf("writes = %d", f.reg.Writes)
	}
}

type failingReg struct{ *winreg.Fake }

func (failingReg) SetBinary(winreg.Root, string, string, []byte) error { return errors.New("denied") }

func TestWindowsStartupWriteFails(t *testing.T) {
	f := newWinFixture(t)
	f.m.Registry = failingReg{f.reg}
	if _, err := f.m.Set(context.Background(), "win:run:discord", false); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("err = %v", err)
	}
}

func TestWindowsStartupTasksFail(t *testing.T) {
	f := newWinFixture(t)
	f.m.Win.Tasks = func(context.Context) (string, error) { return "", errors.New("no schtasks") }
	items, err := f.m.List(context.Background())
	if err != nil || len(items) != 7 {
		t.Fatalf("%d items, %v", len(items), err)
	}
}

func TestApprovedValue(t *testing.T) {
	if !Approved(winreg.Value{}, false) || !Approved(bin(0x02), true) || !Approved(bin(0x06), true) ||
		Approved(bin(0x03), true) || Approved(bin(0x07), true) || !Approved(winreg.Value{Type: winreg.TypeDWord}, true) {
		t.Fatal("Approved")
	}
	on := ApprovedValue(true, time.Now())
	if len(on) != 12 || on[0] != 2 || binary.LittleEndian.Uint64(on[4:]) != 0 {
		t.Fatalf("on = % x", on)
	}
}
