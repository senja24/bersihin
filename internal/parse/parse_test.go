package parse

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "macos", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBundleInfo(t *testing.T) {
	p, err := PlutilJSON([]byte(sample(t, "info_plist.json")))
	if err != nil {
		t.Fatal(err)
	}
	got := BundleInfo(p)
	want := Bundle{Name: "Spotify", ID: "com.spotify.client", Version: "1.2.52.442"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// version falls back to CFBundleVersion
	got = BundleInfo(Plist{"CFBundleVersion": "7"})
	if got.Version != "7" || got.Name != "" {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestPlutilJSONErrors(t *testing.T) {
	for _, in := range []string{"", "not json", "[1,2]", "null", "{"} {
		if _, err := PlutilJSON([]byte(in)); err == nil {
			t.Errorf("PlutilJSON(%q) should fail", in)
		}
	}
}

func TestLaunchAgentInfo(t *testing.T) {
	tests := []struct {
		file  string
		label string
		want  LaunchAgent
		cmd   string
	}{
		{"launchagent_keystone.json", "x", LaunchAgent{
			Label:   "com.google.keystone.agent",
			Program: "/Users/agus/Library/Google/GoogleSoftwareUpdate/GoogleSoftwareUpdate.bundle/Contents/MacOS/GoogleSoftwareUpdateAgent",
			Args: []string{
				"/Users/agus/Library/Google/GoogleSoftwareUpdate/GoogleSoftwareUpdate.bundle/Contents/MacOS/GoogleSoftwareUpdateAgent",
				"-runMode", "ifneeded"},
		}, "/Users/agus/Library/Google/GoogleSoftwareUpdate/GoogleSoftwareUpdate.bundle/Contents/MacOS/GoogleSoftwareUpdateAgent -runMode ifneeded"},
		{"launchagent_program.json", "x", LaunchAgent{
			Label: "com.spotify.webhelper", Program: "/Applications/Spotify.app/Contents/MacOS/SpotifyWebHelper", Disabled: true,
		}, "/Applications/Spotify.app/Contents/MacOS/SpotifyWebHelper"},
		{"launchagent_bad.json", "fallback", LaunchAgent{Label: "fallback"}, ""},
	}
	for _, tc := range tests {
		p, err := PlutilJSON([]byte(sample(t, tc.file)))
		if err != nil {
			t.Fatal(err)
		}
		got := LaunchAgentInfo(p, tc.label)
		if got.Label != tc.want.Label || got.Program != tc.want.Program || got.Disabled != tc.want.Disabled || !slices.Equal(got.Args, tc.want.Args) {
			t.Errorf("%s: got %+v, want %+v", tc.file, got, tc.want)
		}
		if got.Command() != tc.cmd {
			t.Errorf("%s: command %q, want %q", tc.file, got.Command(), tc.cmd)
		}
	}
}

func TestLaunchctlList(t *testing.T) {
	got := LaunchctlList(sample(t, "launchctl_list.txt"))
	for _, l := range []string{"com.apple.Safari.SafeBrowsing.Service", "com.google.keystone.agent", "com.spotify.webhelper", "com.docker.helper"} {
		if !got[l] {
			t.Errorf("missing %s", l)
		}
	}
	if got["Label"] || len(got) != 4 {
		t.Errorf("header or junk included: %v", got)
	}
	if len(LaunchctlList("")) != 0 {
		t.Error("empty output")
	}
}

func TestLaunchctlPrintDisabled(t *testing.T) {
	got := LaunchctlPrintDisabled(sample(t, "launchctl_print_disabled.txt"))
	want := map[string]bool{
		"com.apple.Siri.agent":  false,
		"com.spotify.webhelper": true,
		"com.old.thing":         true,
		"com.new.thing":         false,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", k, g, ok, v)
		}
	}
}

func TestMount(t *testing.T) {
	entries := Mount(sample(t, "mount.txt"))
	if len(entries) != 15 {
		t.Fatalf("parsed %d entries", len(entries))
	}
	byMount := map[string]MountEntry{}
	for _, e := range entries {
		byMount[e.Mount] = e
	}
	root := byMount["/"]
	if root.Device != "/dev/disk3s1s1" || root.FSType() != "apfs" || !root.Has("local") || !root.Has("read-only") {
		t.Errorf("root: %+v", root)
	}
	d := byMount["/Volumes/Data Drive"]
	if d.Device != "/dev/disk5s1" || !d.Has("local") || d.Has("read-only") {
		t.Errorf("spaced mount point: %+v", d)
	}
	smb := byMount["/Volumes/media"]
	if smb.Device != "//agus@nas.local/media" || smb.FSType() != "smbfs" || smb.Has("local") {
		t.Errorf("smb: %+v", smb)
	}
	if !byMount["/Volumes/Photos SSD"].Has("noowners") || byMount["/Volumes/Photos SSD"].FSType() != "exfat" {
		t.Errorf("exfat: %+v", byMount["/Volumes/Photos SSD"])
	}
	if !byMount["/Volumes/Installer"].Has("mounted by agus") {
		t.Errorf("owner option: %+v", byMount["/Volumes/Installer"])
	}
	if len(Mount("garbage\n\nnot a mount line")) != 0 {
		t.Error("garbage parsed")
	}
}

func TestHdiutilMounts(t *testing.T) {
	got := HdiutilMounts(sample(t, "hdiutil_info.txt"))
	if len(got) != 2 || !got["/Volumes/Installer"] || !got["/Volumes/Work Images"] {
		t.Fatalf("got %v", got)
	}
}

func TestDU(t *testing.T) {
	got := DU(sample(t, "du_sk.txt"))
	want := map[string]int64{
		"/Applications/Safari.app":              1234 * 1024,
		"/Applications/Xcode.app":               204800 * 1024,
		"/Users/agus/Applications/Tiny App.app": 88 * 1024,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if len(DU("du: /nope: No such file or directory\n")) != 0 {
		t.Error("error line parsed")
	}
}

func TestMdlsDates(t *testing.T) {
	// Real bytes from a Mac: unpadded hour, 12-hour clock, U+202F before AM/PM,
	// then a U+00A0 and a plain-space variant, a 24-hour value, "(null)" and a
	// zone that is not UTC.
	raw := sample(t, "mdls_raw.bin")
	if !strings.Contains(raw, "\u202fAM") || !strings.Contains(raw, "\u00a0PM") {
		t.Fatal("fixture lost its non-ASCII spaces")
	}
	want := []int64{
		1788860463000, // 2026-09-08 09:41:03Z
		1792428086000, // 2026-10-19 16:41:26Z
		1792428086000, // U+00A0 variant
		1792428086000, // plain space variant
		1792428086000, // 24-hour value
		0,             // (null)
		1733056200000, // 2024-12-01T18:00:00+05:30 = 12:30Z
	}
	got := MdlsDates(raw, len(want))
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if MdlsDates(raw, len(want)+2) != nil {
		t.Error("a short output must be rejected")
	}
	if got := MdlsDates("2025-03-14 09:26:53 +0000", 1); len(got) != 1 || got[0] != 1741944413000 {
		t.Errorf("single value without NUL: %v", got)
	}
}

func TestMdlsDateForms(t *testing.T) {
	const noon = 1741953600000 // 2025-03-14T12:00:00Z
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"24-hour padded", "2025-03-14 12:00:00 +0000", noon},
		{"24-hour unpadded hour", "2025-03-14 9:26:53 +0000", 1741944413000},
		{"12-hour padded PM", "2025-03-14 12:00:00 PM +0000", noon},
		{"12 PM is noon", "2025-03-14 12:00:00\u202fPM +0000", noon},
		{"12 AM is midnight", "2025-03-14 12:00:00\u202fAM +0000", noon - 12*3600*1000},
		{"1 PM", "2025-03-14 1:00:00 pm +0000", noon + 3600*1000},
		{"11 AM", "2025-03-14 11:00:00 AM +0000", noon - 3600*1000},
		{"nbsp before PM", "2025-03-14 1:00:00\u00a0PM +0000", noon + 3600*1000},
		{"east zone", "2025-03-14 5:30:00 PM +0530", noon},
		{"west zone", "2025-03-14 7:00:00 AM -0500", noon},
		{"surrounding space", "  2025-03-14 12:00:00 +0000\n", noon},
		{"null", "(null)", 0},
		{"empty", "", 0},
		{"hour 13 with PM", "2025-03-14 13:00:00 PM +0000", 0},
		{"hour 0 with AM", "2025-03-14 0:00:00 AM +0000", 0},
		{"hour 25", "2025-03-14 25:00:00 +0000", 0},
		{"minute 61", "2025-03-14 12:61:00 +0000", 0},
		{"garbage", "yesterday", 0},
		{"31 February", "2025-02-31 12:00:00 +0000", 0},
		{"month 13", "2025-13-01 12:00:00 +0000", 0},
		{"no zone", "2025-03-14 12:00:00", 0},
		{"before 1970", "1960-01-01 12:00:00 +0000", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MdlsDates(tc.in, 1)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("MdlsDates(%q) = %v, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func tri(b *bool) string {
	switch {
	case b == nil:
		return "unknown"
	case *b:
		return "on"
	}
	return "off"
}

func TestStatusParsers(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) *bool
		file string
		want string
	}{
		{"spctl on", Spctl, "spctl_enabled.txt", "on"},
		{"spctl off", Spctl, "spctl_disabled.txt", "off"},
		{"spctl garbage", Spctl, "mount.txt", "unknown"},
		{"csrutil on", Csrutil, "csrutil_enabled.txt", "on"},
		{"csrutil off", Csrutil, "csrutil_disabled.txt", "off"},
		{"csrutil custom", Csrutil, "csrutil_custom.txt", "unknown"},
		{"firewall on", SocketFilterFW, "socketfilterfw_on.txt", "on"},
		{"firewall off", SocketFilterFW, "socketfilterfw_off.txt", "off"},
		{"firewall block all", SocketFilterFW, "socketfilterfw_blockall.txt", "on"},
		{"firewall garbage", SocketFilterFW, "du_sk.txt", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tri(tc.fn(sample(t, tc.file))); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
	if tri(Spctl("")) != "unknown" || tri(Csrutil("")) != "unknown" || tri(SocketFilterFW("")) != "unknown" {
		t.Error("empty output must be unknown")
	}
}

func TestPlutilRaw(t *testing.T) {
	if PlutilRaw("  145.3\n") != "145.3" {
		t.Error("trim")
	}
}

// A value that is neither "(null)" nor a date (a non-English locale prints
// other AM/PM words or another layout) is counted, so it is not mistaken for
// "never used".
func TestMdlsDatesCountsUnparsedValues(t *testing.T) {
	in := "2026-10-19 16:41:26 +0000\x00(null)\x00019-10-2026 04:41:26 du. +0000\x002026-10-19 4:41:26 nachm. +0000\x00"
	dates, unparsed := MdlsDatesChecked(in, 4)
	if !slices.Equal(dates, []int64{1792428086000, 0, 0, 0}) {
		t.Errorf("dates = %v", dates)
	}
	if unparsed != 2 {
		t.Errorf("unparsed = %d, want 2 (the null value is not one of them)", unparsed)
	}
	if d, u := MdlsDatesChecked(sample(t, "mdls_raw.bin"), 7); d == nil || u != 0 {
		t.Errorf("real fixture: %v, %d unparsed", d, u)
	}
	if d, u := MdlsDatesChecked("x", 3); d != nil || u != 0 {
		t.Errorf("short output: %v, %d", d, u)
	}
}
