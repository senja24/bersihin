package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/testenv"
)

type fixture struct {
	home     string
	apps     string
	m        *Manager
	trashed  []string
	trashErr error
}

func mkApp(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(p, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{home: home, apps: filepath.Join(home, "Applications")}
	os.MkdirAll(f.apps, 0o755)
	spotify := mkApp(t, f.apps, "Spotify.app")
	safari := mkApp(t, f.apps, "Safari.app")
	mkApp(t, f.apps, "NoPlist.app")
	mkApp(t, filepath.Join(f.apps, "Adobe Photoshop 2025"), "Adobe Photoshop 2025.app")
	mkApp(t, filepath.Join(f.apps, "Utilities"), "Terminal.app")
	mkApp(t, f.apps, ".Hidden.app")
	os.WriteFile(filepath.Join(f.apps, "readme.txt"), nil, 0o644)

	plists := map[string]string{
		spotify: `{"CFBundleDisplayName":"Spotify","CFBundleIdentifier":"com.spotify.client","CFBundleShortVersionString":"1.2.52"}`,
		safari:  `{"CFBundleDisplayName":"Safari","CFBundleIdentifier":"com.apple.Safari","CFBundleVersion":"19618"}`,
		filepath.Join(f.apps, "Adobe Photoshop 2025", "Adobe Photoshop 2025.app"): `{"CFBundleIdentifier":"com.adobe.Photoshop"}`,
	}
	run := sys.RunFunc(func(_ context.Context, name string, args ...string) (sys.Result, error) {
		switch name {
		case "/usr/bin/plutil":
			app := filepath.Dir(filepath.Dir(args[len(args)-1]))
			if j, ok := plists[app]; ok {
				return sys.Result{Stdout: j}, nil
			}
			return sys.Result{}, errors.New("no plist")
		case "/usr/bin/du":
			var b strings.Builder
			for _, p := range args[1:] {
				b.WriteString("100\t" + p + "\n")
			}
			return sys.Result{Stdout: b.String()}, nil
		case "/usr/bin/mdls":
			paths := args[3:]
			vals := make([]string, len(paths))
			for i, p := range paths {
				vals[i] = "(null)"
				if p == spotify {
					vals[i] = "2025-03-14 09:26:53 +0000"
				}
			}
			return sys.Result{Stdout: strings.Join(vals, "\x00") + "\x00"}, nil
		}
		return sys.Result{}, errors.New("unexpected " + name)
	})
	f.m = &Manager{Run: run, Home: home, Roots: []string{"/nonexistent-root", f.apps}, Trash: func(p string) error {
		f.trashed = append(f.trashed, p)
		return f.trashErr
	}}
	return f
}

func byName(apps []model.InstalledApp, name string) *model.InstalledApp {
	for i := range apps {
		if apps[i].Name == name {
			return &apps[i]
		}
	}
	return nil
}

func TestList(t *testing.T) {
	f := newFixture(t)
	apps, err := f.m.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range apps {
		names = append(names, a.Name)
	}
	want := []string{"Adobe Photoshop 2025", "NoPlist", "Safari", "Spotify"}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	sp := byName(apps, "Spotify")
	if sp.Version != "1.2.52" || sp.Size != 100*1024 || sp.LastUsed != 1741944413000 || !sp.CanUninstall || sp.Publisher != "" {
		t.Errorf("Spotify = %+v", sp)
	}
	if sp.Path != filepath.Join(f.apps, "Spotify.app") || sp.IconPath != sp.Path || !strings.HasPrefix(sp.ID, "mac:") || len(sp.ID) != 4+16 {
		t.Errorf("Spotify paths/id = %+v", sp)
	}
	sf := byName(apps, "Safari")
	if sf.CanUninstall || sf.Publisher != "Apple" || sf.Version != "19618" || sf.LastUsed != 0 {
		t.Errorf("Safari = %+v", sf)
	}
	if np := byName(apps, "NoPlist"); np == nil || np.Version != "" || np.CanUninstall {
		t.Errorf("NoPlist = %+v", np)
	}
	again, _ := f.m.List(context.Background(), nil)
	if again[3].ID != sp.ID {
		t.Error("ids are not stable")
	}
}

func TestListWithoutRunnerIsEmpty(t *testing.T) {
	got, err := (&Manager{}).List(context.Background(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestUninstall(t *testing.T) {
	f := newFixture(t)
	apps, _ := f.m.List(context.Background(), nil)
	sp := byName(apps, "Spotify")
	sf := byName(apps, "Safari")

	for _, id := range []string{sf.ID, "mac:0000000000000000", "", "../../etc", f.apps} {
		if r := f.m.Uninstall(context.Background(), id); r.OK {
			t.Errorf("Uninstall(%q) succeeded", id)
		}
	}
	if len(f.trashed) != 0 {
		t.Fatalf("trashed %v", f.trashed)
	}

	r := f.m.Uninstall(context.Background(), sp.ID)
	if !r.OK || r.Message != "Moved to Trash. You can put it back from there" {
		t.Fatalf("r = %+v", r)
	}
	if !slices.Equal(f.trashed, []string{sp.Path}) {
		t.Fatalf("trashed = %v", f.trashed)
	}
	if r := f.m.Uninstall(context.Background(), sp.ID); r.OK || len(f.trashed) != 1 {
		t.Error("second uninstall of the same id must not trash again")
	}
}

func TestUninstallRefusals(t *testing.T) {
	f := newFixture(t)
	apps, _ := f.m.List(context.Background(), nil)
	sp := byName(apps, "Spotify")

	// the bundle is swapped for a link after listing
	os.RemoveAll(sp.Path)
	outside := t.TempDir()
	testenv.Symlink(t, outside, sp.Path)
	if r := f.m.Uninstall(context.Background(), sp.ID); r.OK || len(f.trashed) != 0 {
		t.Fatalf("link accepted: %+v trashed %v", r, f.trashed)
	}
	// gone
	os.Remove(sp.Path)
	if r := f.m.Uninstall(context.Background(), sp.ID); r.OK || r.Message != "The app is already gone" {
		t.Fatalf("r = %+v", r)
	}
	// permission failure
	np := byName(apps, "Adobe Photoshop 2025")
	f.trashErr = errors.New("operation not permitted")
	if r := f.m.Uninstall(context.Background(), np.ID); r.OK || !strings.Contains(r.Message, "Drag the app to the Trash") {
		t.Fatalf("r = %+v", r)
	}
}

func TestUninstallNotOnMac(t *testing.T) {
	if r := (&Manager{}).Uninstall(context.Background(), "mac:x"); r.OK {
		t.Fatal("uninstall without a runner")
	}
}

func TestChunks(t *testing.T) {
	got := chunks([]int{1, 2, 3, 4, 5}, 2)
	if len(got) != 3 || len(got[2]) != 1 || len(chunks([]int(nil), 3)) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestUnreadableBundleIsNotRemovable(t *testing.T) {
	f := newFixture(t)
	apps, _ := f.m.List(context.Background(), nil)
	np := byName(apps, "NoPlist")
	if np == nil || np.CanUninstall {
		t.Fatalf("NoPlist = %+v", np)
	}
	if r := f.m.Uninstall(context.Background(), np.ID); r.OK || len(f.trashed) != 0 {
		t.Fatalf("an app whose Info.plist cannot be read was uninstalled: %+v", r)
	}
}

// One app whose du never finishes (here: fails as a timeout does) must not
// take the sizes of the other apps with it.
func TestOneSlowDuDoesNotZeroTheOthers(t *testing.T) {
	f := newFixture(t)
	slow := filepath.Join(f.apps, "Safari.app")
	inner := f.m.Run
	f.m.Run = sys.RunFunc(func(ctx context.Context, name string, args ...string) (sys.Result, error) {
		if name == "/usr/bin/du" && slices.Contains(args, slow) {
			return sys.Result{}, context.DeadlineExceeded
		}
		return inner.Run(ctx, name, args...)
	})
	apps, err := f.m.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sp := byName(apps, "Spotify"); sp == nil || sp.Size != 100*1024 {
		t.Errorf("Spotify = %+v, want its size despite the slow app", sp)
	}
	if ph := byName(apps, "Adobe Photoshop 2025"); ph == nil || ph.Size != 100*1024 {
		t.Errorf("Photoshop = %+v", ph)
	}
	if sf := byName(apps, "Safari"); sf == nil || sf.Size != 0 {
		t.Errorf("Safari = %+v", sf)
	}
}

// H4: a last-used date in a layout we cannot read is counted and logged, not
// silently treated as "never used".
func TestUnreadableLastUsedDatesAreLogged(t *testing.T) {
	f := newFixture(t)
	inner := f.m.Run
	f.m.Run = sys.RunFunc(func(ctx context.Context, name string, args ...string) (sys.Result, error) {
		if name == "/usr/bin/mdls" {
			paths := args[3:]
			vals := make([]string, len(paths))
			for i, p := range paths {
				vals[i] = "(null)"
				if strings.HasSuffix(p, "Spotify.app") {
					vals[i] = "09/03/2026 21:41:03" // another locale's layout
				}
				if strings.HasSuffix(p, "Safari.app") {
					vals[i] = "2026-09-08 9:41:03 AM +0000"
				}
			}
			return sys.Result{Stdout: strings.Join(vals, "\x00") + "\x00"}, nil
		}
		return inner.Run(ctx, name, args...)
	})
	var logged []string
	f.m.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	apps, err := f.m.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sp := byName(apps, "Spotify"); sp.LastUsed != 0 {
		t.Errorf("Spotify.LastUsed = %d", sp.LastUsed)
	}
	if sf := byName(apps, "Safari"); sf.LastUsed == 0 {
		t.Error("the readable 12-hour date was dropped")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "1 ") {
		t.Fatalf("logged = %q, want one line counting 1 unreadable date", logged)
	}
}

func TestReadableLastUsedDatesLogNothing(t *testing.T) {
	f := newFixture(t)
	var logged []string
	f.m.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	if _, err := f.m.List(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(logged) != 0 {
		t.Fatalf("logged = %q", logged)
	}
}
