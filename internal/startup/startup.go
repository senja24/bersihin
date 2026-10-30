// Package startup lists the launch agents that run at login and turns them on
// and off with launchctl.
package startup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/parse"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/walk"
)

// Source is a folder of launch agent plists.
type Source struct {
	Dir      string
	Prefix   string // id prefix: "user", "system"
	Location string // shown to the user
	// Elevated: changing these may need the administrator password.
	Elevated bool
}

// Manager lists and toggles launch agents. Run == nil means "not on a Mac".
type Manager struct {
	Run     sys.Runner
	UID     int
	Sources []Source

	mu      sync.Mutex
	targets map[string]target
}

type target struct {
	label    string
	plist    string
	elevated bool
}

var (
	safeLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)
	safePlist = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,200}\.plist$`)
)

func rx(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

var heavy = rx(
	`(?i)\bteams\b`, `(?i)msteams`, `(?i)discord`, `(?i)spotify`, `(?i)steam`, `(?i)epic ?games`, `\bEA(desktop| app)?\b`, `(?i)\borigin\b`,
	`(?i)battle\.net`, `(?i)onedrive`, `(?i)dropbox`, `(?i)google ?drive`, `(?i)googledrivefs`, `(?i)adobe`, `(?i)creative ?cloud`, `(?i)\bzoom\b`,
	`(?i)slack`, `(?i)skype`, `(?i)\bopera\b`, `(?i)edgeautolaunch`, `(?i)msedge`, `(?i)docker`, `(?i)ubisoft`, `(?i)riot`, `(?i)telegram`, `(?i)whatsapp`,
	`(?i)icloud`, `(?i)nvidia ?broadcast`, `(?i)razer`, `(?i)logi ?options`,
)

var light = rx(
	`(?i)update`, `(?i)updater`, `(?i)helper`, `(?i)crashpad`, `(?i)notifier`, `(?i)securityhealth`, `(?i)\bctfmon\b`, `(?i)\bagent\b`,
	`(?i)\btray\b`, `(?i)launchd?helper`, `(?i)keystone`, `(?i)bluetooth`, `(?i)audio`, `(?i)realtek`, `(?i)synaptics`,
)

// ImpactOf guesses how much an item slows the login down from its label and command.
func ImpactOf(name, command string) model.Impact {
	hay := name + " " + command
	for _, r := range heavy {
		if r.MatchString(hay) {
			return model.ImpactHigh
		}
	}
	for _, r := range light {
		if r.MatchString(hay) {
			return model.ImpactLow
		}
	}
	return model.ImpactMedium
}

// DisplayName names an item after its app, else after the tail of its label.
func DisplayName(label, program string) string {
	if m := appInPath.FindStringSubmatch(program); m != nil {
		return m[1]
	}
	parts := strings.Split(label, ".")
	if len(parts) > 2 {
		if n := strings.Join(parts[2:], " "); n != "" {
			return n
		}
	}
	return label
}

var appInPath = regexp.MustCompile(`/([^/]+)\.app/`)

func (m *Manager) run(ctx context.Context, d time.Duration, name string, args ...string) (sys.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return m.Run.Run(ctx, name, args...)
}

func (m *Manager) readSource(ctx context.Context, src Source, loaded map[string]bool, overrides map[string]bool) ([]model.StartupItem, map[string]target) {
	list, err := os.ReadDir(src.Dir)
	if err != nil {
		return nil, nil
	}
	var cands []os.DirEntry
	for _, e := range list {
		if strings.HasSuffix(e.Name(), ".plist") && e.Type()&os.ModeSymlink == 0 {
			cands = append(cands, e)
		}
	}
	var (
		mu    sync.Mutex
		items []model.StartupItem
		tgts  = map[string]target{}
	)
	// at most 8 plutil processes at a time
	walk.ForEach(ctx, len(cands), 8, func(i int) {
		e := cands[i]
		full := filepath.Join(src.Dir, e.Name())
		res, err := m.run(ctx, 8*time.Second, "/usr/bin/plutil", "-convert", "json", "-o", "-", full)
		if err != nil {
			return
		}
		plist, err := parse.PlutilJSON([]byte(res.Stdout))
		if err != nil {
			return
		}
		a := parse.LaunchAgentInfo(plist, strings.TrimSuffix(e.Name(), ".plist"))
		command := a.Command()
		enabled := loaded[a.Label]
		if !enabled {
			if over, ok := overrides[a.Label]; ok {
				enabled = !over
			} else {
				enabled = !a.Disabled
			}
		}
		id := "mac:" + src.Prefix + ":" + a.Label
		// Elevated commands are built from the label and file name, so only plain ones qualify.
		canToggle := !src.Elevated || (safeLabel.MatchString(a.Label) && safePlist.MatchString(e.Name()))
		item := model.StartupItem{
			ID: id, Name: DisplayName(a.Label, a.Program), Command: command, Location: src.Location, Enabled: enabled,
			Impact: ImpactOf(a.Label, command), CanToggle: canToggle, NeedsAdmin: src.Elevated,
		}
		mu.Lock()
		items = append(items, item)
		if canToggle {
			tgts[id] = target{label: a.Label, plist: full, elevated: src.Elevated}
		}
		mu.Unlock()
	})
	return items, tgts
}

// List returns the launch agents, heaviest first, and refreshes the table of
// what can be toggled.
func (m *Manager) List(ctx context.Context) ([]model.StartupItem, error) {
	items := []model.StartupItem{}
	if m.Run == nil {
		return items, nil
	}
	var loaded, overrides map[string]bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		res, _ := m.run(ctx, 15*time.Second, "/bin/launchctl", "list")
		loaded = parse.LaunchctlList(res.Stdout)
	}()
	go func() {
		defer wg.Done()
		res, _ := m.run(ctx, 15*time.Second, "/bin/launchctl", "print-disabled", "gui/"+strconv.Itoa(m.UID))
		overrides = parse.LaunchctlPrintDisabled(res.Stdout)
	}()
	wg.Wait()

	targets := map[string]target{}
	for _, src := range m.Sources {
		got, tgts := m.readSource(ctx, src, loaded, overrides)
		items = append(items, got...)
		for k, v := range tgts {
			targets[k] = v
		}
	}
	m.mu.Lock()
	m.targets = targets
	m.mu.Unlock()

	rank := map[model.Impact]int{model.ImpactHigh: 0, model.ImpactMedium: 1, model.ImpactLow: 2}
	slices.SortStableFunc(items, func(a, b model.StartupItem) int {
		if rank[a.Impact] != rank[b.Impact] {
			return rank[a.Impact] - rank[b.Impact]
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items, nil
}

// ShQuote quotes s for /bin/sh: single quotes, with each ' written as '\”.
func ShQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// AppleScriptString quotes s as an AppleScript string literal.
func AppleScriptString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// ElevatedScript is the AppleScript for toggling a system-wide agent with the
// administrator password: the documented use of osascript. Label and plist
// come from files on disk, so both are quoted for sh and then for AppleScript.
func ElevatedScript(enabled bool, domain, service, plist string) string {
	var cmd string
	if enabled {
		cmd = fmt.Sprintf("/bin/launchctl enable %s && (/bin/launchctl bootstrap %s %s || /bin/launchctl print %s >/dev/null 2>&1)", ShQuote(service), ShQuote(domain), ShQuote(plist), ShQuote(service))
	} else {
		cmd = fmt.Sprintf("/bin/launchctl disable %s && (/bin/launchctl bootout %s %s || ! /bin/launchctl print %s >/dev/null 2>&1)", ShQuote(service), ShQuote(domain), ShQuote(plist), ShQuote(service))
	}
	return "do shell script " + AppleScriptString(cmd) + " with administrator privileges"
}

// loaded asks launchctl whether the agent is loaded right now.
func (m *Manager) loaded(ctx context.Context, label string) bool {
	res, _ := m.run(ctx, 15*time.Second, "/bin/launchctl", "list")
	return parse.LaunchctlList(res.Stdout)[label]
}

// Set turns an item on or off and returns the new list. The page sends the id
// of an item from List, never a label or a path.
func (m *Manager) Set(ctx context.Context, id string, enabled bool) ([]model.StartupItem, error) {
	if m.Run == nil {
		return nil, errors.New("Startup items are not supported on this system")
	}
	if len(id) > 600 {
		return nil, errors.New("Invalid request")
	}
	m.mu.Lock()
	empty := m.targets == nil
	m.mu.Unlock()
	if empty {
		if _, err := m.List(ctx); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	t, ok := m.targets[id]
	m.mu.Unlock()
	if !ok {
		return nil, errors.New("This item cannot be changed from Bersihin")
	}

	// LaunchAgents (also the all-users ones) run in each user's GUI domain, not in "system".
	domain := "gui/" + strconv.Itoa(m.UID)
	service := domain + "/" + t.label
	verb, apply := "disable", "bootout"
	if enabled {
		verb, apply = "enable", "bootstrap"
	}
	// enable/disable persists across logins; bootstrap/bootout applies it right now.
	if _, err := m.run(ctx, 15*time.Second, "/bin/launchctl", verb, service); err == nil {
		_, applyErr := m.run(ctx, 15*time.Second, "/bin/launchctl", apply, domain, t.plist)
		if applyErr != nil && m.loaded(ctx, t.label) != enabled {
			// "already loaded" and "no such process" are benign: the agent is
			// in the state that was asked for. Anything else is reported. The
			// enable/disable override is saved either way; the contract has
			// no field for a warning, so the page gets the error and its next
			// List shows the saved state.
			return nil, fmt.Errorf("The change was saved, but macOS could not apply it now (launchctl %s failed). It takes effect at the next login.", apply)
		}
		return m.List(ctx)
	}
	if !t.elevated {
		return nil, errors.New("macOS did not allow this change")
	}
	if !safeLabel.MatchString(t.label) || !safePlist.MatchString(filepath.Base(t.plist)) {
		return nil, errors.New("This item cannot be changed from Bersihin")
	}
	script := ElevatedScript(enabled, domain, service, t.plist)
	if _, err := m.run(ctx, 180*time.Second, "/usr/bin/osascript", "-e", script); err != nil {
		return nil, errors.New("macOS did not allow the change")
	}
	return m.List(ctx)
}
