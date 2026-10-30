package protection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

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

var now = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

func checker(t *testing.T, spctl, fw, sip string, xprotect map[string]time.Time) *Checker {
	run := sys.RunFunc(func(_ context.Context, name string, args ...string) (sys.Result, error) {
		switch name {
		case "/usr/sbin/spctl":
			return sys.Result{Stdout: spctl}, nil
		case "/usr/libexec/ApplicationFirewall/socketfilterfw":
			return sys.Result{Stdout: fw}, nil
		case "/usr/bin/csrutil":
			return sys.Result{Stdout: sip}, nil
		case "/usr/bin/plutil":
			if _, ok := xprotect[args[len(args)-1]]; ok {
				return sys.Result{Stdout: "5312\n"}, nil
			}
		}
		return sys.Result{}, errors.New("unexpected " + name)
	})
	return &Checker{
		Run: run, Now: func() time.Time { return now },
		ModTime: func(p string) (time.Time, error) {
			if t, ok := xprotect[p]; ok {
				return t, nil
			}
			return time.Time{}, os.ErrNotExist
		},
	}
}

func TestStatusAllGood(t *testing.T) {
	c := checker(t, sample(t, "spctl_enabled.txt"), sample(t, "socketfilterfw_on.txt"), sample(t, "csrutil_enabled.txt"),
		map[string]time.Time{xprotectPlists[1]: now.Add(-3 * day)})
	st := c.Status(context.Background())
	if !st.Enabled || st.Provider != "XProtect + Gatekeeper" || st.Realtime == nil || !*st.Realtime ||
		st.Firewall == nil || !*st.Firewall || st.LastScan != nil || st.Score != 100 {
		t.Fatalf("status = %+v", st)
	}
	if st.DefinitionsUpdated == nil || *st.DefinitionsUpdated != now.Add(-3*day).UnixMilli() {
		t.Errorf("updated = %v", st.DefinitionsUpdated)
	}
	want := []string{
		"Gatekeeper is on", "Firewall is on", "System Integrity Protection is on", "XProtect 5312 updated 3 days ago",
	}
	if !slices.Equal(st.Notes, want) {
		t.Errorf("notes = %v", st.Notes)
	}
}

func TestStatusBad(t *testing.T) {
	c := checker(t, sample(t, "spctl_disabled.txt"), sample(t, "socketfilterfw_off.txt"), sample(t, "csrutil_disabled.txt"), nil)
	st := c.Status(context.Background())
	if st.Enabled || st.Realtime != nil || st.DefinitionsUpdated != nil || st.Score != 0 {
		t.Fatalf("status = %+v", st)
	}
	want := []string{
		"Gatekeeper is off", "Firewall is off", "System Integrity Protection is off", "Could not find XProtect",
	}
	if !slices.Equal(st.Notes, want) {
		t.Errorf("notes = %v", st.Notes)
	}
}

func TestStatusUnknownOutputs(t *testing.T) {
	c := checker(t, "", "", sample(t, "csrutil_custom.txt"), map[string]time.Time{xprotectPlists[0]: now.Add(-60 * day)})
	st := c.Status(context.Background())
	if st.Firewall != nil {
		t.Errorf("firewall = %v", *st.Firewall)
	}
	// XProtect present and Gatekeeper unreadable: still "enabled"
	if !st.Enabled {
		t.Error("should count as enabled")
	}
	// 20 (xprotect) + 12 (sip unknown) + 5 (60 days old) = 37
	if st.Score != 37 {
		t.Errorf("score = %d", st.Score)
	}
	want := []string{"Could not read Gatekeeper status", "Could not read firewall status", "XProtect 5312 updated 60 days ago"}
	if !slices.Equal(st.Notes, want) {
		t.Errorf("notes = %v", st.Notes)
	}
}

func TestNewestXProtectWins(t *testing.T) {
	c := checker(t, "", "", "", map[string]time.Time{
		xprotectPlists[0]: now.Add(-40 * day),
		xprotectPlists[1]: now.Add(-1 * day),
		xprotectPlists[2]: now.Add(-100 * day),
	})
	st := c.Status(context.Background())
	if *st.DefinitionsUpdated != now.Add(-1*day).UnixMilli() || st.Notes[len(st.Notes)-1] != "XProtect 5312 updated yesterday" {
		t.Fatalf("status = %+v", st)
	}
}

func TestNotOnMac(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("New() runs the real macOS tools here; this test is for other systems")
	}
	st := New().Status(context.Background())
	if st.Provider != "Unknown" || st.Enabled || st.Score != 0 || len(st.Notes) != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestAge(t *testing.T) {
	c := &Checker{Now: func() time.Time { return now }}
	for d, want := range map[time.Duration]string{0: "today", 5 * time.Hour: "today", 30 * time.Hour: "yesterday", 3 * day: "3 days ago"} {
		if got := c.age(now.Add(-d).UnixMilli()); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
}
