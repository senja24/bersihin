package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/settings"
	"dev.agus.bersihin/internal/snapshot"
	"dev.agus.bersihin/internal/testenv"
)

type fakeShell struct {
	mu       sync.Mutex
	trashed  []string
	revealed []string
	opened   []string
	// onTrash, when set, runs before an item is recorded (it may block).
	onTrash func(p string)
}

func (f *fakeShell) TrashItem(p string) error {
	if f.onTrash != nil {
		f.onTrash(p)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trashed = append(f.trashed, p)
	return nil
}
func (f *fakeShell) ShowItemInFolder(p string) { f.revealed = append(f.revealed, p) }
func (f *fakeShell) OpenExternal(u string) error {
	f.opened = append(f.opened, u)
	return nil
}
func (f *fakeShell) FileIcon(string, int) ([]byte, error) { return []byte("png"), nil }

type env struct {
	home  string
	shell *fakeShell
	svc   Services
	mu    sync.Mutex
	prog  []model.ScanProgress
	clock time.Time
}

func (e *env) now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// advance moves the fake clock that tokens, snapshots and junk cutoffs use.
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.clock = e.clock.Add(d)
	e.mu.Unlock()
}

var runCount atomic.Int64

// rid makes a valid, unused run id like the page does.
func rid() string { return fmt.Sprintf("test-run-%012d", runCount.Add(1)) }

func newEnv(t *testing.T) *env {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	e := &env{home: home, shell: &fakeShell{}, clock: time.Now()}
	e.svc = New(Deps{
		Home: home, Shell: e.shell, AppVersion: func() string { return "0.1.0" }, Now: e.now,
		Sink: func(p model.ScanProgress) { e.mu.Lock(); e.prog = append(e.prog, p); e.mu.Unlock() },
	})
	e.svc.Files.c.trasher.Guard = testGuard(home)
	return e
}

func put(t *testing.T, p string, size int, age time.Duration) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	os.Chtimes(p, ts, ts)
}

func TestInfo(t *testing.T) {
	e := newEnv(t)
	info := e.svc.System.Info(context.Background())
	if info.HomeDir != e.home || info.AppVersion != "0.1.0" || info.Arch == "" || info.Platform == "" {
		t.Fatalf("info = %+v", info)
	}
}

func TestRevealPath(t *testing.T) {
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Downloads", "a.zip"), 1, 0)
	if err := e.svc.System.RevealPath("~/Downloads/a.zip"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e.shell.revealed, []string{filepath.Join(e.home, "Downloads", "a.zip")}) {
		t.Fatalf("revealed = %v", e.shell.revealed)
	}
	for _, bad := range []string{"", "relative", "//host/share/x", "/does/not/exist", "bad\x00path", "~/missing", "/NET/x", "/net/server/share", "/volumes/Unknown/x", "/Volumes/NotMounted"} {
		if err := e.svc.System.RevealPath(bad); err == nil {
			t.Errorf("RevealPath(%q) should fail", bad)
		}
	}
	if len(e.shell.revealed) != 1 {
		t.Errorf("revealed bad paths: %v", e.shell.revealed)
	}
}

func TestOpenExternal(t *testing.T) {
	e := newEnv(t)
	for _, ok := range config.ExternalLinks {
		if err := e.svc.System.OpenExternal(ok); err != nil {
			t.Fatalf("OpenExternal(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"https://example.com/docs", "https://console.anthropic.com/settings/keys/../../x", "https://console.anthropic.com/settings/keys?x=1", "https://console.anthropic.com.evil.example/settings/keys", "http://example.com", "file:///etc/passwd", "javascript:alert(1)", "mailto:a@b.c", "https://", "", "not a url", "x-apple.systempreferences:com.apple.preference"} {
		if err := e.svc.System.OpenExternal(bad); err == nil {
			t.Errorf("OpenExternal(%q) should fail", bad)
		}
	}
	if !slices.Equal(e.shell.opened, config.ExternalLinks) {
		t.Fatalf("opened = %v", e.shell.opened)
	}
}

// testGuard is the Mac guard with the system roots that hold home removed: on
// macOS t.TempDir() lives in /private/var/folders, which the real guard blocks
// as a system root, so the success path could never run there.
func testGuard(home string) func(context.Context) pathsafe.Guard {
	return func(context.Context) pathsafe.Guard {
		g := pathsafe.NewGuard(home, nil)
		var roots []string
		for _, r := range g.SystemRoots {
			if !g.Inside(r, home) && !g.Same(r, home) {
				roots = append(roots, r)
			}
		}
		g.SystemRoots = roots
		return g
	}
}

// inject remembers a large files scan holding exactly these paths, with the
// size and modification time they have now, as if the scan had found them.
func (e *env) inject(t *testing.T, paths ...string) string {
	t.Helper()
	var files []model.LargeFile
	for _, p := range paths {
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, model.LargeFile{Path: p, Size: fi.Size(), Modified: fi.ModTime().UnixMilli()})
	}
	id := rid()
	e.svc.Files.c.snaps.Put(snapshot.Large(id, files))
	return id
}

// trash asks for a token the way the page does, then moves the paths.
func (e *env) trash(scanID string, paths []string) (model.CleanResult, error) {
	tok, err := e.svc.Confirm.Trash(scanID, paths)
	if err != nil {
		return model.CleanResult{}, err
	}
	return e.svc.Files.TrashPaths(context.Background(), rid(), scanID, paths, tok)
}

// scanJunk runs a junk scan and returns its scan id.
func (e *env) scanJunk(t *testing.T) (string, []model.JunkCategory) {
	t.Helper()
	id := rid()
	cats, err := e.svc.Junk.Scan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return id, cats
}

func (e *env) clean(scanID string, ids []string) (model.CleanResult, error) {
	tok, err := e.svc.Confirm.Clean(scanID, ids)
	if err != nil {
		return model.CleanResult{}, err
	}
	return e.svc.Junk.Clean(context.Background(), rid(), scanID, ids, tok)
}

// The path guard still runs on paths a scan returned: a snapshot that holds
// dangerous paths cannot get them trashed.
func TestTrashPathsGoesThroughTheGuard(t *testing.T) {
	e := newEnv(t)
	a := filepath.Join(e.home, "Downloads", "a.zip")
	put(t, a, 10, 0)
	put(t, filepath.Join(e.home, ".ssh", "id_rsa"), 1, 0)
	system := "/etc/passwd"
	if testenv.Windows {
		system = filepath.Join(os.Getenv("SystemRoot"), "win.ini")
	}
	paths := []string{a, filepath.Join(e.home, ".ssh", "id_rsa"), e.home, system}
	res, err := e.trash(e.inject(t, paths...), paths)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 || res.Freed != 10 || res.Failed != 3 {
		t.Fatalf("res = %+v", res)
	}
	if !slices.Equal(e.shell.trashed, []string{a}) {
		t.Fatalf("trashed = %v", e.shell.trashed)
	}
}

func TestJunkScanAndCancelScan(t *testing.T) {
	testenv.MacLayout(t)
	e := newEnv(t)
	for i := range 6 {
		put(t, filepath.Join(e.home, "Library", "Logs", "app"+string(rune('a'+i)), "x.log"), 100, 48*time.Hour)
	}
	scanID := rid()
	cats, err := e.svc.Junk.Scan(context.Background(), scanID)
	if err != nil || len(cats) != 1 || cats[0].ID != "mac-logs" || cats[0].Size != 600 || cats[0].Locations[0] != "~/Library/Logs" {
		t.Fatalf("cats = %+v err = %v", cats, err)
	}
	e.mu.Lock()
	last := e.prog[len(e.prog)-1]
	for _, p := range e.prog {
		if p.Task != model.TaskJunk || p.ID != scanID {
			t.Errorf("task = %q id = %q", p.Task, p.ID)
		}
	}
	e.mu.Unlock()
	if last.Phase != "Done" {
		t.Errorf("last progress = %+v", last)
	}

	// cancelling from the progress callback ends the scan with ErrCancelled
	cancelled := newEnv(t)
	put(t, filepath.Join(cancelled.home, "Library", "Logs", "a", "x"), 1, 0)
	cancelled.svc = New(Deps{
		Home: cancelled.home, Shell: cancelled.shell,
		Sink: func(model.ScanProgress) {},
	})
	svc := cancelled.svc
	svc.System.c.deps.Sink = func(p model.ScanProgress) { svc.System.CancelScan(p.ID) }
	cancelID := rid()
	if _, err := svc.Junk.Scan(context.Background(), cancelID); err != progress.ErrCancelled {
		t.Fatalf("err = %v", err)
	}
	if svc.System.CancelScan(cancelID) {
		t.Error("nothing should be running any more")
	}
}

func TestJunkCleanOnlyReceivesIDs(t *testing.T) {
	testenv.MacLayout(t)
	e := newEnv(t)
	log := filepath.Join(e.home, "Library", "Logs", "app", "x.log")
	keep := filepath.Join(e.home, "Documents", "x.txt")
	put(t, log, 100, 48*time.Hour)
	put(t, keep, 100, 48*time.Hour)
	scanID, _ := e.scanJunk(t)
	// ids that are not categories of that scan cannot even get a token
	for _, bad := range []string{"/Users", "../Documents", filepath.Join(e.home, "Documents")} {
		if _, err := e.svc.Confirm.Clean(scanID, []string{"mac-logs", bad}); err == nil {
			t.Errorf("token issued for %q", bad)
		}
	}
	res, err := e.clean(scanID, []string{"mac-logs"})
	if err != nil || res.Freed != 100 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("log survived")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("document was deleted")
	}
}

func TestScansReturnData(t *testing.T) {
	testenv.MacLayout(t)
	e := newEnv(t)
	put(t, filepath.Join(e.home, "Documents", "report.pdf"), 2000, 0)
	put(t, filepath.Join(e.home, "Movies", "film.mov"), 3*1024*1024, 0)
	put(t, filepath.Join(e.home, "Movies", "film copy.mov"), 3*1024*1024, 0)

	node, err := e.svc.Files.ScanSpaces(context.Background(), rid(), "")
	if err != nil || node.Name != "Home" || len(node.Children) == 0 {
		t.Fatalf("spaces = %+v err = %v", node, err)
	}
	if _, err := e.svc.Files.ScanSpaces(context.Background(), rid(), "/Volumes/Nope"); err == nil {
		t.Error("unknown volume accepted")
	}
	large, err := e.svc.Files.ScanLargeFiles(context.Background(), rid(), 2*1024*1024)
	if err != nil || len(large.Files) != 2 || large.Hidden != 0 {
		t.Fatalf("large = %+v err = %v", large, err)
	}
	dupes, err := e.svc.Files.ScanDuplicates(context.Background(), rid())
	groups := dupes.Groups
	// the two files hold zeros, so they are identical; a sparse filesystem may skip them
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if len(g.Files) != 2 || g.Size != 3*1024*1024 {
			t.Errorf("group = %+v", g)
		}
	}
	if disks, err := e.svc.System.Disks(context.Background()); err != nil || len(disks) == 0 || !disks[0].System {
		t.Errorf("disks = %+v err = %v", disks, err)
	}
	icons, err := e.svc.System.FileIcons(context.Background(), []string{filepath.Join(e.home, "Documents")})
	if err != nil || len(icons) != 1 {
		t.Errorf("icons = %v err = %v", icons, err)
	}
}

func TestMachineServicesOffMac(t *testing.T) {
	e := newEnv(t)
	apps, err := e.svc.Apps.List(context.Background(), rid())
	if err != nil || apps == nil {
		t.Fatalf("apps = %v err = %v", apps, err)
	}
	listID := rid()
	if _, err := e.svc.Apps.List(context.Background(), listID); err != nil {
		t.Fatal(err)
	}
	if r, err := e.svc.Apps.Uninstall(context.Background(), listID, "mac:abc", ""); err == nil || r.OK {
		t.Error("uninstall without a token")
	}
	items, err := e.svc.Startup.List(context.Background())
	if err != nil || items == nil {
		t.Fatalf("startup = %v err = %v", items, err)
	}
	tok, err := e.svc.Confirm.Startup("mac:user:x", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Startup.Set(context.Background(), "mac:user:x", true, tok); err == nil {
		t.Error("startup set off a Mac")
	}
	if st := e.svc.Protection.Status(context.Background()); st.Provider == "" || st.Notes == nil {
		t.Errorf("protection = %+v", st)
	}
}

type memKeychain struct{ m map[string][]byte }

func (k *memKeychain) Get(key string) ([]byte, error) {
	if v, ok := k.m[key]; ok {
		return v, nil
	}
	return nil, settings.ErrNotFound
}
func (k *memKeychain) Set(key string, v []byte) error { k.m[key] = v; return nil }
func (k *memKeychain) Delete(key string) error        { delete(k.m, key); return nil }

func TestSettingsAndAssistantEndToEnd(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-api-key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"content":[{"type":"text","text":"{\"answer\":\"Fine.\",\"suggestions\":[]}"}]}`)
	}))
	defer srv.Close()

	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "data")
	kc := &memKeychain{m: map[string][]byte{}}
	svc := New(Deps{
		Home: home, Shell: &fakeShell{}, DataDir: func() (string, error) { return dir, nil }, Secrets: kc,
		AnthropicURL: srv.URL, OpenAIURL: srv.URL,
	})

	if _, err := svc.Assistant.Ask(context.Background(), "hi", model.AiContext{}); err == nil || !strings.Contains(err.Error(), "Add your Claude API key") {
		t.Fatalf("err = %v", err)
	}
	const key = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
	got, err := svc.Settings.SetApiKey(model.ProviderClaude, &[]string{key}[0], "")
	if err != nil || !got.HasClaudeKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	mb := 800
	got, err = svc.Settings.Set(model.SettingsPatch{LargeFileMinMb: &mb})
	if err != nil || got.LargeFileMinMb != 800 || svc.Settings.Get().LargeFileMinMb != 800 {
		t.Fatalf("got %+v err %v", got, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(b), key) {
		t.Fatal("key written to settings.json")
	}

	adv, err := svc.Assistant.Ask(context.Background(), "what can go from "+home+"/Library?", model.AiContext{
		LargeFiles: []model.AiLargeFile{{Path: home + "/Movies/a.mkv", Size: 1 << 30}},
	})
	if err != nil || adv.Answer != "Fine." || adv.Provider != model.ProviderClaude {
		t.Fatalf("adv = %+v err %v", adv, err)
	}
	if gotAuth != key {
		t.Errorf("api key header = %q", gotAuth)
	}
	// ~/Movies/a.mkv; ~\Movies\a.mkv on Windows, its backslashes escaped in the JSON
	slashed := regexp.MustCompile(`\\+`).ReplaceAllString(gotBody, "/")
	if strings.Contains(gotBody, home) || strings.Contains(slashed, filepath.ToSlash(home)) || !strings.Contains(slashed, "~/Movies/a.mkv") {
		t.Errorf("home folder not redacted: %s", gotBody)
	}
}

func TestCompatibleProviderEndToEnd(t *testing.T) {
	var hits int
	var gotAuth, gotPath, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth, gotPath, gotUA = r.Header.Get("authorization"), r.URL.Path, r.Header.Get("user-agent")
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"answer\":\"Fine.\",\"suggestions\":[]}"}}]}`)
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "data")
	kc := &memKeychain{m: map[string][]byte{}}
	svc := New(Deps{
		Home: t.TempDir(), Shell: &fakeShell{}, DataDir: func() (string, error) { return dir, nil }, Secrets: kc,
		AppVersion: func() string { return "9.9.9" },
	})
	ctx := context.Background()
	p := model.ProviderOpenAICompat
	if _, err := svc.Settings.Set(model.SettingsPatch{AiProvider: &p}); err != nil {
		t.Fatal(err)
	}

	// not set up: a clear message, nothing sent
	if _, err := svc.Assistant.Ask(ctx, "hi", model.AiContext{}); err == nil || !strings.Contains(err.Error(), "Set up your OpenAI-compatible provider") {
		t.Fatalf("err = %v", err)
	}
	res, err := svc.Assistant.TestConnection(ctx, p, nil)
	if err != nil || res.OK || res.Code != "bad_url" {
		t.Fatalf("res %+v err %v", res, err)
	}

	base, mdl := srv.URL+"/v1", "llama-3"
	got, err := svc.Settings.Set(model.SettingsPatch{OpenAiCompat: &model.AiEndpointPatch{BaseUrl: &base, Model: &mdl}})
	if err != nil || got.OpenAiCompat.BaseUrl != base || got.OpenAiCompat.HasKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	if res, _ = svc.Assistant.TestConnection(ctx, p, nil); res.Code != "no_key" {
		t.Fatalf("res %+v", res)
	}
	const key = "local-key"
	if got, err = svc.Settings.SetApiKey(p, &[]string{key}[0], svc.Settings.Get().OpenAiCompat.BaseUrl); err != nil || !got.OpenAiCompat.HasKey || got.HasOpenAiKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	if res, err = svc.Assistant.TestConnection(ctx, p, nil); err != nil || !res.OK || res.Mode != "structured" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if gotAuth != "Bearer "+key || gotPath != "/v1/chat/completions" || gotUA != "Bersihin/9.9.9" {
		t.Errorf("auth %q path %q ua %q", gotAuth, gotPath, gotUA)
	}
	adv, err := svc.Assistant.Ask(ctx, "hi", model.AiContext{})
	if err != nil || adv.Answer != "Fine." || adv.Provider != p {
		t.Fatalf("adv %+v err %v", adv, err)
	}

	// the key never reaches the page or the settings file
	b, _ := json.Marshal(svc.Settings.Get())
	f, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(b)+string(f), key) {
		t.Fatal("key leaked")
	}

	// a new host drops the key; testing a draft on that host does not send it
	before := hits
	other := "https://other.example.org/v1"
	got, err = svc.Settings.Set(model.SettingsPatch{OpenAiCompat: &model.AiEndpointPatch{BaseUrl: &other}})
	if err != nil || got.OpenAiCompat.HasKey || kc.m[config.OpenAICompatKeyName] != nil {
		t.Fatalf("got %+v err %v", got, err)
	}
	if res, _ = svc.Assistant.TestConnection(ctx, p, &model.AiEndpointPatch{BaseUrl: &base}); res.OK || hits != before {
		t.Fatalf("res %+v hits %d->%d", res, before, hits)
	}
}
