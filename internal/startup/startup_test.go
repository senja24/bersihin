package startup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/sys"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "macos", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type call struct {
	name string
	args []string
}

type fixture struct {
	user, system string
	m            *Manager
	mu           sync.Mutex
	calls        []call
	failEnable   bool // launchctl enable/disable fails (needs admin)
	failOSA      bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{user: filepath.Join(base, "user"), system: filepath.Join(base, "system")}
	os.MkdirAll(f.user, 0o755)
	os.MkdirAll(f.system, 0o755)
	touch := func(dir, name string) {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch(f.user, "com.google.keystone.agent.plist")
	touch(f.user, "com.spotify.webhelper.plist")
	touch(f.user, "bad.plist")
	touch(f.user, "notes.txt")
	touch(f.system, "com.google.keystone.agent.plist") // same label as the user's
	os.Symlink(filepath.Join(f.user, "bad.plist"), filepath.Join(f.user, "link.plist"))
	// a label no quoting rule would let through elevated
	touch(f.system, "weird name'$(x).plist")

	plists := map[string]string{
		"com.google.keystone.agent.plist": sample(t, "launchagent_keystone.json"),
		"com.spotify.webhelper.plist":     sample(t, "launchagent_program.json"),
		"bad.plist":                       "not json",
		"weird name'$(x).plist":           `{"Label":"com.evil.x'$(touch /tmp/pwn)","ProgramArguments":["/bin/true"]}`,
	}
	run := sys.RunFunc(func(_ context.Context, name string, args ...string) (sys.Result, error) {
		f.mu.Lock()
		f.calls = append(f.calls, call{name, slices.Clone(args)})
		f.mu.Unlock()
		switch name {
		case "/usr/bin/plutil":
			if j, ok := plists[filepath.Base(args[len(args)-1])]; ok {
				return sys.Result{Stdout: j}, nil
			}
			return sys.Result{}, errors.New("no such plist")
		case "/bin/launchctl":
			switch args[0] {
			case "list":
				return sys.Result{Stdout: sample(t, "launchctl_list.txt")}, nil
			case "print-disabled":
				return sys.Result{Stdout: sample(t, "launchctl_print_disabled.txt")}, nil
			case "enable", "disable":
				if f.failEnable {
					return sys.Result{ExitCode: 1}, errors.New("Operation not permitted")
				}
			}
			return sys.Result{}, nil
		case "/usr/bin/osascript":
			if f.failOSA {
				return sys.Result{}, errors.New("User canceled")
			}
			return sys.Result{}, nil
		}
		return sys.Result{}, errors.New("unexpected " + name)
	})
	f.m = &Manager{
		Run: run, UID: 501,
		Sources: []Source{
			{Dir: f.user, Prefix: "user", Location: "LaunchAgents"},
			{Dir: filepath.Join(base, "missing"), Prefix: "nope", Location: "x"},
			{Dir: f.system, Prefix: "system", Location: "LaunchAgents (all users)", Elevated: true},
		},
	}
	return f
}

func (f *fixture) recorded(name string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func find(items []model.StartupItem, id string) *model.StartupItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestList(t *testing.T) {
	f := newFixture(t)
	items, err := f.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %+v", items)
	}
	keystone := find(items, "mac:user:com.google.keystone.agent")
	if keystone == nil || keystone.Name != "keystone agent" {
		t.Fatalf("keystone = %+v", keystone)
	}
	if keystone.Impact != model.ImpactLow || !keystone.Enabled || keystone.CanToggle != true || keystone.NeedsAdmin ||
		keystone.Location != "LaunchAgents" || !strings.HasSuffix(keystone.Command, "GoogleSoftwareUpdateAgent -runMode ifneeded") {
		t.Errorf("keystone = %+v", keystone)
	}
	spotify := find(items, "mac:user:com.spotify.webhelper")
	// loaded in launchctl list, so on even though the plist says Disabled and print-disabled says disabled
	if spotify == nil || spotify.Name != "Spotify" || spotify.Impact != model.ImpactHigh || !spotify.Enabled {
		t.Errorf("spotify = %+v", spotify)
	}
	sys := find(items, "mac:system:com.google.keystone.agent")
	if sys == nil || !sys.NeedsAdmin || !sys.CanToggle || sys.Location != "LaunchAgents (all users)" {
		t.Errorf("system keystone = %+v", sys)
	}
	var weird *model.StartupItem
	for i := range items {
		if strings.Contains(items[i].ID, "evil") {
			weird = &items[i]
		}
	}
	if weird == nil || weird.CanToggle {
		t.Errorf("an unquotable elevated agent must not be toggleable: %+v", weird)
	}
	// heaviest first
	if items[0].Impact != model.ImpactHigh {
		t.Errorf("order = %+v", items)
	}
	if len(f.recorded("/bin/launchctl")) < 2 {
		t.Error("launchctl not consulted")
	}
	for _, c := range f.recorded("/bin/launchctl") {
		if c.args[0] == "print-disabled" && c.args[1] != "gui/501" {
			t.Errorf("print-disabled args = %v", c.args)
		}
	}
}

func TestListEnabledFromOverridesAndPlist(t *testing.T) {
	// nothing loaded: the override decides, else the plist's Disabled key
	f2 := newFixture(t)
	orig := f2.m.Run
	f2.m.Run = sys.RunFunc(func(ctx context.Context, name string, args ...string) (sys.Result, error) {
		if name == "/bin/launchctl" && args[0] == "list" {
			return sys.Result{Stdout: "PID\tStatus\tLabel\n"}, nil
		}
		if name == "/bin/launchctl" && args[0] == "print-disabled" {
			return sys.Result{Stdout: "disabled services = {\n\t\"com.google.keystone.agent\" => disabled\n}\n"}, nil
		}
		return orig.Run(ctx, name, args...)
	})
	items, _ := f2.m.List(context.Background())
	if k := find(items, "mac:user:com.google.keystone.agent"); k == nil || k.Enabled {
		t.Errorf("override should disable keystone: %+v", k)
	}
	if s := find(items, "mac:user:com.spotify.webhelper"); s == nil || s.Enabled {
		t.Errorf("plist Disabled=true should disable spotify: %+v", s)
	}
}

func TestSetPlainAgent(t *testing.T) {
	f := newFixture(t)
	items, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", false)
	if err != nil || len(items) != 4 {
		t.Fatalf("items = %v err = %v", items, err)
	}
	var verbs [][]string
	for _, c := range f.recorded("/bin/launchctl") {
		if c.args[0] != "list" && c.args[0] != "print-disabled" {
			verbs = append(verbs, c.args)
		}
	}
	plist := filepath.Join(f.user, "com.spotify.webhelper.plist")
	want := [][]string{{"disable", "gui/501/com.spotify.webhelper"}, {"bootout", "gui/501", plist}}
	if !slices.EqualFunc(verbs, want, slices.Equal[[]string]) {
		t.Fatalf("launchctl calls = %v, want %v", verbs, want)
	}
	if len(f.recorded("/usr/bin/osascript")) != 0 {
		t.Error("no password prompt expected")
	}

	f.calls = nil
	if _, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", true); err != nil {
		t.Fatal(err)
	}
	cs := f.recorded("/bin/launchctl")
	if cs[0].args[0] != "enable" && cs[2].args[0] != "enable" {
		t.Errorf("calls = %v", cs)
	}
}

func TestSetRefusesUnknownAndFailing(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"", "mac:user:com.nope", "mac:user:../../x", strings.Repeat("a", 601)} {
		if _, err := f.m.Set(context.Background(), id, false); err == nil {
			t.Errorf("Set(%q) should fail", id)
		}
	}
	f.failEnable = true
	if _, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", false); err == nil || err.Error() != "macOS did not allow this change" {
		t.Errorf("err = %v", err)
	}
	if len(f.recorded("/usr/bin/osascript")) != 0 {
		t.Error("a user agent must never prompt for the password")
	}
	// the unquotable agent is not toggleable at all
	for _, c := range []string{"mac:system:com.evil.x'$(touch /tmp/pwn)"} {
		if _, err := f.m.Set(context.Background(), c, false); err == nil {
			t.Error("unquotable agent was toggled")
		}
	}
	if _, err := (&Manager{}).Set(context.Background(), "x", true); err == nil {
		t.Error("not on a Mac")
	}
}

func TestSetElevatedFallsBackToOsascript(t *testing.T) {
	f := newFixture(t)
	f.failEnable = true
	items, err := f.m.Set(context.Background(), "mac:system:com.google.keystone.agent", false)
	if err != nil || items == nil {
		t.Fatalf("err = %v", err)
	}
	osa := f.recorded("/usr/bin/osascript")
	if len(osa) != 1 || len(osa[0].args) != 2 || osa[0].args[0] != "-e" {
		t.Fatalf("osascript calls = %v", osa)
	}
	script := osa[0].args[1]
	plist := filepath.Join(f.system, "com.google.keystone.agent.plist")
	for _, want := range []string{
		`do shell script "`, `with administrator privileges`, `/bin/launchctl disable 'gui/501/com.google.keystone.agent'`,
		`/bin/launchctl bootout 'gui/501' '` + plist + `'`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}

	f.failOSA = true
	if _, err := f.m.Set(context.Background(), "mac:system:com.google.keystone.agent", true); err == nil {
		t.Error("a cancelled prompt must fail")
	}
}

func TestQuoting(t *testing.T) {
	shell := map[string]string{
		`plain`:           `'plain'`,
		`with space`:      `'with space'`,
		`it's`:            `'it'\''s'`,
		`'; rm -rf ~`:     `''\''; rm -rf ~'`,
		`$(id) ` + "`id`": `'$(id) ` + "`id`'",
		``:                `''`,
	}
	for in, want := range shell {
		if got := ShQuote(in); got != want {
			t.Errorf("ShQuote(%q) = %s, want %s", in, got, want)
		}
	}
	osa := map[string]string{
		`plain`:      `"plain"`,
		`say "hi"`:   `"say \"hi\""`,
		`back\slash`: `"back\\slash"`,
		`\"`:         `"\\\""`,
	}
	for in, want := range osa {
		if got := AppleScriptString(in); got != want {
			t.Errorf("AppleScriptString(%q) = %s, want %s", in, got, want)
		}
	}
	// A hostile path survives both layers: after AppleScript unescaping the
	// shell sees one single-quoted word.
	script := ElevatedScript(true, "gui/501", "gui/501/com.a.b", `/Library/LaunchAgents/x' ; touch /tmp/pwn ; '.plist`)
	if !strings.HasPrefix(script, `do shell script "`) || !strings.HasSuffix(script, `" with administrator privileges`) {
		t.Fatalf("script = %s", script)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(script, `do shell script "`), `" with administrator privileges`)
	inner = strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(inner)
	if !strings.Contains(inner, `'/Library/LaunchAgents/x'\'' ; touch /tmp/pwn ; '\''.plist'`) {
		t.Fatalf("hostile path not neutralised: %s", inner)
	}
}

func TestImpactAndName(t *testing.T) {
	tests := []struct {
		label, command string
		impact         model.Impact
	}{
		{"com.spotify.webhelper", "/Applications/Spotify.app/x", model.ImpactHigh},
		{"com.microsoft.teams", "x", model.ImpactHigh},
		{"com.docker.helper", "x", model.ImpactHigh},
		{"com.google.keystone.agent", "x", model.ImpactLow},
		{"com.apple.something", "/usr/libexec/x", model.ImpactMedium},
		{"com.foo.updater", "x", model.ImpactLow},
		{"com.foo.bar", "/usr/bin/true", model.ImpactMedium},
	}
	for _, tc := range tests {
		if got := ImpactOf(tc.label, tc.command); got != tc.impact {
			t.Errorf("ImpactOf(%q, %q) = %q, want %q", tc.label, tc.command, got, tc.impact)
		}
	}
	names := []struct{ label, program, want string }{
		{"com.spotify.webhelper", "/Applications/Spotify.app/Contents/MacOS/SpotifyWebHelper", "Spotify"},
		{"com.google.keystone.agent", "/Users/a/Library/Google/x", "keystone agent"},
		{"com.foo", "/usr/bin/x", "com.foo"},
		{"agent", "", "agent"},
	}
	for _, tc := range names {
		if got := DisplayName(tc.label, tc.program); got != tc.want {
			t.Errorf("DisplayName(%q, %q) = %q, want %q", tc.label, tc.program, got, tc.want)
		}
	}
}

func TestNotOnMac(t *testing.T) {
	items, err := New("/Users/a").List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Fatal("nil list")
	}
}

func TestPlutilRunsAreBounded(t *testing.T) {
	dir := t.TempDir()
	for i := range 40 {
		os.WriteFile(filepath.Join(dir, "com.x.a"+string(rune('a'+i%26))+string(rune('a'+i/26))+".plist"), nil, 0o644)
	}
	var cur, peak atomic.Int32
	m := &Manager{UID: 1, Sources: []Source{{Dir: dir, Prefix: "user", Location: "x"}}, Run: sys.RunFunc(
		func(_ context.Context, name string, args ...string) (sys.Result, error) {
			if name != "/usr/bin/plutil" {
				return sys.Result{}, nil
			}
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			return sys.Result{Stdout: `{"Label":"com.x.` + filepath.Base(args[len(args)-1]) + `"}`}, nil
		})}
	items, _ := m.List(context.Background())
	if len(items) != 40 || peak.Load() > 8 {
		t.Fatalf("items %d, peak plutil runs %d", len(items), peak.Load())
	}
}

// withLaunchctl wraps the fixture's runner: fn may answer a launchctl call, or
// return ok=false to fall through to the fixture.
func withLaunchctl(f *fixture, fn func(args []string) (sys.Result, error, bool)) {
	orig := f.m.Run
	f.m.Run = sys.RunFunc(func(ctx context.Context, name string, args ...string) (sys.Result, error) {
		if name == "/bin/launchctl" {
			if r, err, ok := fn(args); ok {
				return r, err
			}
		}
		return orig.Run(ctx, name, args...)
	})
}

func TestSetReportsBootoutFailureWhileItemStillRuns(t *testing.T) {
	f := newFixture(t)
	withLaunchctl(f, func(args []string) (sys.Result, error, bool) {
		if args[0] == "bootout" {
			return sys.Result{ExitCode: 5}, errors.New("Input/output error"), true
		}
		return sys.Result{}, nil, false
	})
	if _, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", false); err == nil {
		t.Fatal("a failed bootout of a running agent was swallowed")
	}
}

func TestSetIgnoresBootoutFailureWhenNothingWasRunning(t *testing.T) {
	f := newFixture(t)
	withLaunchctl(f, func(args []string) (sys.Result, error, bool) {
		switch args[0] {
		case "list":
			return sys.Result{Stdout: "PID\tStatus\tLabel\n"}, nil, true
		case "print-disabled":
			return sys.Result{Stdout: "disabled services = {\n}\n"}, nil, true
		case "bootout":
			return sys.Result{ExitCode: 113}, errors.New("Could not find specified service"), true
		}
		return sys.Result{}, nil, false
	})
	// the plist's Disabled key makes spotify "off" when nothing is loaded
	if _, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", false); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func notLoadedButEnabled(args []string) (sys.Result, error, bool) {
	switch args[0] {
	case "list":
		return sys.Result{Stdout: "PID\tStatus\tLabel\n"}, nil, true
	case "print-disabled":
		// what `launchctl enable` leaves behind
		return sys.Result{Stdout: "disabled services = {\n\t\"com.spotify.webhelper\" => enabled\n}\n"}, nil, true
	}
	return sys.Result{}, nil, false
}

func TestSetReportsBootstrapFailureWhenNotLoaded(t *testing.T) {
	f := newFixture(t)
	withLaunchctl(f, func(args []string) (sys.Result, error, bool) {
		if args[0] == "bootstrap" {
			return sys.Result{ExitCode: 5}, errors.New("Bootstrap failed: 5: Input/output error"), true
		}
		return notLoadedButEnabled(args)
	})
	_, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", true)
	if err == nil || !strings.Contains(err.Error(), "saved") {
		t.Fatalf("a failed bootstrap of an unloaded agent was not reported: %v", err)
	}
}

func TestSetIgnoresBootstrapFailureWhenAlreadyLoaded(t *testing.T) {
	f := newFixture(t) // the sample `launchctl list` has spotify loaded
	withLaunchctl(f, func(args []string) (sys.Result, error, bool) {
		if args[0] == "bootstrap" {
			return sys.Result{ExitCode: 5}, errors.New("Bootstrap failed: 5: Input/output error"), true
		}
		return sys.Result{}, nil, false
	})
	if _, err := f.m.Set(context.Background(), "mac:user:com.spotify.webhelper", true); err != nil {
		t.Fatalf("already loaded is benign: %v", err)
	}
}

func TestElevatedScriptDoesNotMaskFailures(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		script := ElevatedScript(enabled, "gui/501", "gui/501/com.a.b", "/Library/LaunchAgents/com.a.b.plist")
		if strings.Contains(script, "|| true") {
			t.Errorf("enabled=%v: script ignores bootstrap/bootout failures:\n%s", enabled, script)
		}
		if !strings.Contains(script, "launchctl print 'gui/501/com.a.b'") {
			t.Errorf("enabled=%v: script does not check the loaded state:\n%s", enabled, script)
		}
	}
}
