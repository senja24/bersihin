package icons

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/testenv"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake")

func TestIcons(t *testing.T) {
	home := testenv.TempDir(t)
	app := filepath.Join(home, "Applications", "Foo.app")
	os.MkdirAll(app, 0o755)
	doc := filepath.Join(home, "doc.pdf")
	os.WriteFile(doc, []byte("x"), 0o644)

	var calls atomic.Int32
	var sizes []int
	var smu sync.Mutex
	p := &Provider{
		Home: home,
		Icon: func(path string, size int) ([]byte, error) {
			calls.Add(1)
			smu.Lock()
			sizes = append(sizes, size)
			smu.Unlock()
			if strings.HasSuffix(path, "doc.pdf") {
				return nil, errors.New("no icon")
			}
			return pngBytes, nil
		},
		LocalMounts: func(context.Context) []string { return []string{"/Volumes/Data"} },
	}
	got, err := p.Icons(context.Background(), []string{
		app, "~/Applications/Foo.app", doc,
		filepath.Join(home, "missing.app"),
		"relative/path", "", "//host/share/x", "/net/server/x", "/Network/x", "/home/user/x",
		"/Volumes/Unknown/x", "bad\x00path", app,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if len(got) != 2 || got[app] != want || got["~/Applications/Foo.app"] != want {
		t.Fatalf("got %v", got)
	}
	if calls.Load() != 2 { // the app once for both spellings, the document once
		t.Errorf("Icon called %d times", calls.Load())
	}
	for _, s := range sizes {
		if s != Size {
			t.Errorf("size = %d", s)
		}
	}
	// second request is served from the cache
	before := calls.Load()
	p.Icons(context.Background(), []string{app})
	if calls.Load() != before {
		t.Error("cache not used")
	}
}

func TestVolumesNeedALocalMount(t *testing.T) {
	testenv.MacPaths(t)
	var asked []string
	p := &Provider{
		Home:        "/Users/a",
		Icon:        func(path string, _ int) ([]byte, error) { asked = append(asked, path); return pngBytes, nil },
		LocalMounts: func(context.Context) []string { return []string{"/Volumes/Data"} },
	}
	mounts := []string(nil)
	for path, want := range map[string]bool{
		"/Volumes/Data":            true,
		"/Volumes/data/x":          true,
		"/Volumes/Data2/x":         false,
		"/Volumes/media/x":         false,
		"/Applications/Safari.app": true,
	} {
		if got := p.candidate(context.Background(), path, &mounts) != ""; got != want {
			t.Errorf("candidate(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestTooManyPaths(t *testing.T) {
	p := &Provider{Icon: func(string, int) ([]byte, error) { return pngBytes, nil }}
	paths := make([]string, maxPaths+1)
	if _, err := p.Icons(context.Background(), paths); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSlowIconTimesOutAndIsNotCached(t *testing.T) {
	dir := testenv.TempDir(t)
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, nil, 0o644)
	release := make(chan struct{})
	defer close(release)
	var n atomic.Int32
	p := &Provider{
		Home: dir, Timeout: 20 * time.Millisecond,
		Icon: func(string, int) ([]byte, error) {
			if n.Add(1) == 1 {
				<-release
			}
			return pngBytes, nil
		},
	}
	got, _ := p.Icons(context.Background(), []string{f})
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	// the retry may take its time: on Windows the existence check alone can outlast 20 ms
	p.Timeout = 5 * time.Second
	got, _ = p.Icons(context.Background(), []string{f})
	if len(got) != 1 {
		t.Fatalf("retry failed: %v", got)
	}
}

func TestCacheIsBounded(t *testing.T) {
	p := &Provider{}
	for i := range maxCache + 50 {
		p.remember("/p/"+string(rune(i+1000)), "u")
	}
	if len(p.cache) != maxCache || len(p.order) != maxCache {
		t.Fatalf("cache = %d order = %d", len(p.cache), len(p.order))
	}
}

func TestCandidateIsCaseAndFormInsensitive(t *testing.T) {
	testenv.MacPaths(t)
	p := &Provider{Home: "/Users/a", LocalMounts: func(context.Context) []string { return []string{"/Volumes/Data"} }}
	for path, want := range map[string]bool{
		"/net/x": false, "/NET/x": false, "/Net": false, "/NETWORK/srv": false, "/Home/user": false, "/HOME": false,
		"/net‌/x":          false, // invisible character
		"/volumes/Media/x": false, "/VOLUMES/data/x": true, "/Volumes/DATA": true,
		"/Volumes/Data/../Media/x": true, // cleaned to /Volumes/Media/x first? see below
		"/homes/x":                 true, "/network-share": true, "/Applications/Safari.app": true,
	} {
		got := p.Candidate(context.Background(), path) != ""
		if path == "/Volumes/Data/../Media/x" {
			want = false // Clean makes it /Volumes/Media/x, which is not a local mount
		}
		if got != want {
			t.Errorf("Candidate(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestNegativeResultsAreCachedButTimeoutsAreNot(t *testing.T) {
	dir := testenv.TempDir(t)
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, nil, 0o644)
	var n atomic.Int32
	p := &Provider{Home: dir, Icon: func(string, int) ([]byte, error) { n.Add(1); return nil, errors.New("none") }}
	for range 3 {
		p.Icons(context.Background(), []string{f})
	}
	if n.Load() != 1 {
		t.Fatalf("a path with no icon was asked %d times", n.Load())
	}
}

func TestStuckLookupsAreBounded(t *testing.T) {
	dir := testenv.TempDir(t)
	release := make(chan struct{})
	defer close(release)
	var started atomic.Int32
	p := &Provider{Home: dir, Timeout: 30 * time.Millisecond, Icon: func(string, int) ([]byte, error) {
		started.Add(1)
		<-release
		return nil, nil
	}}
	var paths []string
	for i := range 30 {
		f := filepath.Join(dir, "f"+string(rune('a'+i)))
		os.WriteFile(f, nil, 0o644)
		paths = append(paths, f)
	}
	for range 3 {
		p.Icons(context.Background(), paths)
	}
	if started.Load() > maxInFlight {
		t.Fatalf("%d lookups hung at once, limit %d", started.Load(), maxInFlight)
	}
}

func TestDataVolumeAliasesOfNetworkFoldersAreRefused(t *testing.T) {
	testenv.MacPaths(t)
	p := &Provider{Home: "/Users/a", LocalMounts: func(context.Context) []string { return []string{"/Volumes/Data"} }}
	for _, path := range []string{
		"/System/Volumes/Data/Volumes/Media/x", "/system/volumes/data/volumes/Data/x", "/System/Volumes/Data/Volumes",
		"/System/Volumes/Data/Network/srv", "/System/Volumes/Data/net/x", "/System/Volumes/Data/home/u",
		"/private/var/automount/Network/x", "/PRIVATE/var/automount", "/System/Volumes/Data/private/var/automount/x",
	} {
		if p.Candidate(context.Background(), path) != "" {
			t.Errorf("Candidate(%q) accepted", path)
		}
	}
	if p.Candidate(context.Background(), "/System/Volumes/Data/Users/a/x") == "" {
		t.Error("an ordinary Data volume path was refused")
	}
}

func TestExistenceCheckIsTimeLimited(t *testing.T) {
	testenv.MacPaths(t)
	release := make(chan struct{})
	defer close(release)
	p := &Provider{
		Timeout: 30 * time.Millisecond,
		Exists:  func(string) bool { <-release; return false }, // a dead mount
		Icon: func(string, int) ([]byte, error) {
			t.Error("Icon called for a path that could not be probed")
			return nil, nil
		},
	}
	start := time.Now()
	if p.Present("/Volumes/Dead/x") {
		t.Error("a hung probe counted as present")
	}
	got, _ := p.Icons(context.Background(), []string{"/Applications/Foo.app"})
	if len(got) != 0 || time.Since(start) > 3*time.Second {
		t.Fatalf("got %v after %v", got, time.Since(start))
	}
	// missing paths are not remembered as "no icon"
	later := filepath.Join(t.TempDir(), "Later.app")
	q := &Provider{Exists: func(string) bool { return false }, Icon: func(string, int) ([]byte, error) { return pngBytes, nil }}
	q.Icons(context.Background(), []string{later})
	q.Exists = func(string) bool { return true }
	if got, _ := q.Icons(context.Background(), []string{later}); len(got) != 1 {
		t.Fatalf("a missing path was cached: %v", got)
	}
}

func TestVarAutomountAndFoldedSegments(t *testing.T) {
	testenv.MacPaths(t)
	p := &Provider{Home: "/Users/a"}
	// /var is /private/var on macOS
	for _, path := range []string{
		"/var/automount", "/var/automount/host/share", "/VAR/automount/x",
		"/System/Volumes/Data/var/automount/x",
	} {
		if p.Candidate(context.Background(), path) != "" {
			t.Errorf("Candidate(%q) accepted", path)
		}
	}
	// a segment that folds to ".." or "." must not slip past the name rules
	const zw = "..\u200C"
	for _, path := range []string{
		"/net/" + zw + "/x", "/Volumes/Data/" + zw + "/Media/x", "/tmp/" + zw + "/net/x",
		"/net/.\u200C/x", "/Applications/\u200C/Foo.app", "/Applications/../net/x",
	} {
		if p.Candidate(context.Background(), path) != "" {
			t.Errorf("Candidate(%q) accepted", path)
		}
	}
	if p.Candidate(context.Background(), "/var/log/system.log") == "" {
		t.Error("an ordinary /var path was refused")
	}
}

func TestParentLinkIntoARefusedFolderIsCaughtInTheSlot(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	net := filepath.Join(base, "net")
	os.MkdirAll(filepath.Join(net, "host", "share"), 0o755)
	alias := filepath.Join(base, "alias")
	if err := testenv.Symlink(t, net, alias); err != nil {
		t.Skip(err)
	}
	saved := autofsNames
	autofsNames = append(append([]string{}, saved...), net)
	defer func() { autofsNames = saved }()

	var asked atomic.Int32
	p := &Provider{Icon: func(string, int) ([]byte, error) { asked.Add(1); return pngBytes, nil }}
	target := filepath.Join(alias, "host", "share")
	if p.Candidate(context.Background(), target) == "" {
		t.Fatal("test setup: the lexical rules should not know the alias")
	}
	if p.Present(target) {
		t.Error("Present accepted a path whose parent resolves into a refused folder")
	}
	if got, _ := p.Icons(context.Background(), []string{target}); len(got) != 0 || asked.Load() != 0 {
		t.Errorf("icon fetched through the alias: %v (asked %d)", got, asked.Load())
	}
	// the same folder reached directly is refused lexically too
	if p.Candidate(context.Background(), filepath.Join(net, "host")) != "" {
		t.Error("direct path accepted")
	}
}
