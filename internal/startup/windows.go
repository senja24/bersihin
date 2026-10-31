package startup

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/winreg"
)

// The Windows side of the Manager: what Task Manager's Startup apps tab lists.
// It is platform-neutral, so its rules run and are tested on any OS against
// winreg.Fake; startup_windows.go plugs in the real registry and folders.
//
// Items are turned on and off the way Task Manager does it, by the
// StartupApproved value of the item: the Run value or the shortcut itself is
// never touched. Only the current user's items are changed; items of every
// user (HKEY_LOCAL_MACHINE, the all-users Startup folder) would need
// administrator rights, so they are listed read-only and never elevated.
// Logon tasks of the Task Scheduler are listed read-only too.

const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	runKey32    = `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved`
)

// WinSources are the places a Windows Manager reads.
type WinSources struct {
	// UserFolder and CommonFolder are the Startup folders of the user
	// (%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup) and of every
	// user (%ProgramData%\...\StartUp). Empty: not read.
	UserFolder, CommonFolder string
	// Tasks returns the output of `schtasks /query /xml ONE`. Nil: no tasks.
	Tasks func(ctx context.Context) (string, error)
}

// winTarget is where a toggleable item's StartupApproved value lives.
type winTarget struct {
	approved string // key below HKCU
	value    string
}

// winSource is one place items come from.
type winSource struct {
	prefix, location string
	user             bool // HKCU, changeable; else every user's, read-only
	approved         string
}

// Approved reads a StartupApproved value: Task Manager writes 12 bytes, the
// first even (02, 06) for on and odd (03, 07) for off. No value means on.
func Approved(v winreg.Value, ok bool) bool {
	if !ok || v.Type != winreg.TypeBinary || len(v.Bin) == 0 {
		return true
	}
	return v.Bin[0]&1 == 0
}

// ApprovedValue is the value Task Manager writes: 02 for on, 03 and the time
// of the change (a FILETIME) for off.
func ApprovedValue(enabled bool, now time.Time) []byte {
	b := make([]byte, 12)
	if enabled {
		b[0] = 0x02
		return b
	}
	b[0] = 0x03
	// FILETIME: 100 ns intervals since 1601-01-01 UTC
	ft := uint64(now.UTC().UnixNano()/100) + 116444736000000000
	binary.LittleEndian.PutUint64(b[4:], ft)
	return b
}

func (m *Manager) winApproved(root winreg.Root, key string) map[string]winreg.Value {
	v, err := m.Registry.Values(root, approvedKey+`\`+key)
	if err != nil {
		return map[string]winreg.Value{}
	}
	return v
}

func lookupFold(vals map[string]winreg.Value, name string) (winreg.Value, bool) {
	if v, ok := vals[name]; ok {
		return v, true
	}
	for k, v := range vals {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return winreg.Value{}, false
}

func winRoot(user bool) winreg.Root {
	if user {
		return winreg.CurrentUser
	}
	return winreg.LocalMachine
}

func (m *Manager) item(src winSource, name, display, command string, approved map[string]winreg.Value) (model.StartupItem, winTarget) {
	v, ok := lookupFold(approved, name)
	item := model.StartupItem{
		ID: "win:" + src.prefix + ":" + strings.ToLower(name), Name: display, Command: command, Location: src.location,
		Enabled: Approved(v, ok), Impact: ImpactOf(name, command), CanToggle: src.user, NeedsAdmin: !src.user,
	}
	return item, winTarget{approved: approvedKey + `\` + src.approved, value: name}
}

// runItems lists the values of a Run key.
func (m *Manager) runItems(src winSource, key string) ([]model.StartupItem, map[string]winTarget) {
	root := winRoot(src.user)
	vals, err := m.Registry.Values(root, key)
	if err != nil {
		return nil, nil
	}
	approved := m.winApproved(root, src.approved)
	var items []model.StartupItem
	tgts := map[string]winTarget{}
	for name, v := range vals {
		if name == "" || len(name) > 260 || (v.Type != winreg.TypeString && v.Type != winreg.TypeExpandString) {
			continue
		}
		cmd := strings.TrimSpace(v.Str)
		if cmd == "" {
			continue
		}
		it, t := m.item(src, name, runName(name, cmd), cmd, approved)
		items = append(items, it)
		tgts[it.ID] = t
	}
	return items, tgts
}

// runName names a Run value after the program it starts when the value's
// own name is a GUID or a path, else after the value.
func runName(name, cmd string) string {
	if !strings.HasPrefix(name, "{") && !strings.ContainsAny(name, `\/`) {
		return name
	}
	c := strings.Trim(cmd, `" `)
	if i := strings.Index(strings.ToLower(c), ".exe"); i > 0 {
		c = c[:i]
	}
	if j := strings.LastIndexAny(c, `\/`); j >= 0 {
		c = c[j+1:]
	}
	if c == "" {
		return name
	}
	return c
}

// folderItems lists the shortcuts and programs of a Startup folder.
func (m *Manager) folderItems(src winSource, dir string) ([]model.StartupItem, map[string]winTarget) {
	if dir == "" {
		return nil, nil
	}
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	approved := m.winApproved(winRoot(src.user), src.approved)
	var items []model.StartupItem
	tgts := map[string]winTarget{}
	for _, e := range list {
		n := e.Name()
		if e.IsDir() || strings.EqualFold(n, "desktop.ini") || strings.HasPrefix(n, ".") || len(n) > 260 {
			continue
		}
		display := strings.TrimSuffix(n, filepath.Ext(n))
		it, t := m.item(src, n, display, filepath.Join(dir, n), approved)
		items = append(items, it)
		tgts[it.ID] = t
	}
	return items, tgts
}

// The parts of a Task Scheduler task Bersihin reads.
type xmlTask struct {
	Settings struct {
		Enabled *bool `xml:"Enabled"`
		Hidden  bool  `xml:"Hidden"`
	} `xml:"Settings"`
	Triggers struct {
		Logon []struct {
			Enabled *bool `xml:"Enabled"`
		} `xml:"LogonTrigger"`
	} `xml:"Triggers"`
	Actions struct {
		Exec []struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

// LogonTasks reads the output of `schtasks /query /xml ONE`: the tasks that
// run a program at logon, outside Windows' own \Microsoft folder. Each task
// follows a comment with its path. Bytes that are not UTF-8 (schtasks writes
// the console's code page) are replaced, not an error.
func LogonTasks(out string) []model.StartupItem {
	dec := xml.NewDecoder(strings.NewReader(strings.ToValidUTF8(out, "\uFFFD")))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	dec.Strict = false
	var items []model.StartupItem
	path := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.Comment:
			path = strings.TrimSpace(string(bytes.TrimSpace(t)))
		case xml.StartElement:
			if t.Name.Local != "Task" {
				continue
			}
			var task xmlTask
			if dec.DecodeElement(&task, &t) != nil {
				continue
			}
			name := path
			path = ""
			if !strings.HasPrefix(name, `\`) || strings.HasPrefix(strings.ToLower(name), `\microsoft\`) || len(task.Actions.Exec) == 0 {
				continue
			}
			logon := false
			for _, l := range task.Triggers.Logon {
				if l.Enabled == nil || *l.Enabled {
					logon = true
				}
			}
			if !logon {
				continue
			}
			ex := task.Actions.Exec[0]
			cmd := strings.TrimSpace(strings.TrimSpace(ex.Command) + " " + strings.TrimSpace(ex.Arguments))
			display := name[strings.LastIndexByte(name, '\\')+1:]
			items = append(items, model.StartupItem{
				ID: "win:task:" + strings.ToLower(name), Name: display, Command: cmd, Location: "Task Scheduler",
				Enabled: task.Settings.Enabled == nil || *task.Settings.Enabled, Impact: ImpactOf(display, cmd),
				CanToggle: false, NeedsAdmin: true,
			})
		}
	}
	return items
}

func (m *Manager) winSources() []winSource {
	return []winSource{
		{prefix: "run", location: "Run key", user: true, approved: "Run"},
		{prefix: "run-all", location: "Run key (all users)", approved: "Run"},
		{prefix: "run32-all", location: "Run key (all users, 32-bit)", approved: "Run32"},
		{prefix: "folder", location: "Startup folder", user: true, approved: "StartupFolder"},
		{prefix: "folder-all", location: "Startup folder (all users)", approved: "StartupFolder"},
	}
}

// listWindows is List on Windows.
func (m *Manager) listWindows(ctx context.Context) ([]model.StartupItem, error) {
	return m.listWindowsTasks(ctx, true)
}

// listWindowsTasks lists the items, the logon tasks only when tasks is set:
// they are read-only, so a toggle's own checks need not run schtasks.
func (m *Manager) listWindowsTasks(ctx context.Context, tasks bool) ([]model.StartupItem, error) {
	var items []model.StartupItem
	targets := map[string]winTarget{}
	add := func(got []model.StartupItem, tgts map[string]winTarget) {
		for _, it := range got {
			if slices.ContainsFunc(items, func(o model.StartupItem) bool { return o.ID == it.ID }) {
				continue
			}
			items = append(items, it)
			if it.CanToggle {
				targets[it.ID] = tgts[it.ID]
			}
		}
	}
	src := m.winSources()
	add(m.runItems(src[0], runKey))
	add(m.runItems(src[1], runKey))
	add(m.runItems(src[2], runKey32))
	add(m.folderItems(src[3], m.Win.UserFolder))
	add(m.folderItems(src[4], m.Win.CommonFolder))
	if tasks && m.Win.Tasks != nil {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, err := m.Win.Tasks(cctx)
		cancel()
		if err == nil {
			add(LogonTasks(out), nil)
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	m.mu.Lock()
	m.win = targets
	m.mu.Unlock()
	return sortItems(items), nil
}

// setWindows writes the StartupApproved value of a current-user item, then
// reads the list again to check the item now has that state.
func (m *Manager) setWindows(ctx context.Context, id string, enabled bool) ([]model.StartupItem, error) {
	// the item must still be there as listed: List refreshes the table
	if _, err := m.listWindowsTasks(ctx, false); err != nil {
		return nil, err
	}
	m.mu.Lock()
	t, ok := m.win[id]
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("This item cannot be changed from Bersihin")
	}
	if err := m.Registry.SetBinary(winreg.CurrentUser, t.approved, t.value, ApprovedValue(enabled, m.clock())); err != nil {
		return nil, errors.New("Windows did not allow this change")
	}
	items, err := m.listWindows(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id && it.Enabled != enabled {
			return nil, errors.New("Windows did not keep this change")
		}
	}
	return items, nil
}

func (m *Manager) clock() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
