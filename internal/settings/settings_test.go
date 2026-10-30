package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/endpoint"
	"dev.agus.bersihin/internal/model"
)

type memSecrets struct {
	mu     sync.Mutex
	m      map[string][]byte
	gets   int
	setErr error
	getErr error
	delErr error
	onCall func()   // runs inside every call, before the answer
	calls  []string // "set:name" and "del:name", in order
}

func (s *memSecrets) Get(k string) ([]byte, error) {
	if s.onCall != nil {
		s.onCall()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	v, ok := s.m[k]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}
func (s *memSecrets) Set(k string, v []byte) error {
	if s.onCall != nil {
		s.onCall()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "set:"+k)
	if s.setErr != nil {
		return s.setErr
	}
	if s.m == nil {
		s.m = map[string][]byte{}
	}
	s.m[k] = v
	return nil
}
func (s *memSecrets) Delete(k string) error {
	if s.onCall != nil {
		s.onCall()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "del:"+k)
	if s.delErr != nil {
		return s.delErr
	}
	delete(s.m, k)
	return nil
}

func newStore(t *testing.T) (*Store, *memSecrets, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "userdata") // does not exist yet
	sec := &memSecrets{}
	return &Store{Dir: func() (string, error) { return dir, nil }, Secrets: sec}, sec, dir
}

// noEndpoint is an unset compatible provider as the page sees it.
var noEndpoint = model.AiEndpoint{KeyState: model.KeyNone}

func ptr[T any](v T) *T { return &v }

const goodKey = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"

func TestDefaults(t *testing.T) {
	s, _, dir := newStore(t)
	got := s.Get()
	want := model.Settings{DefaultClaudeModel: config.ClaudeModel, DefaultOpenAiModel: config.OpenAIModel, ClaudeCompat: noEndpoint, OpenAiCompat: noEndpoint, AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("reading settings must not create files")
	}
}

func TestPatchPersistsAtomically(t *testing.T) {
	s, _, dir := newStore(t)
	got, err := s.Patch(model.SettingsPatch{
		AiProvider: ptr(model.ProviderChatGPT), Background: ptr(model.BackgroundGrain),
		LargeFileMinMb: ptr(250), ConfirmBeforeClean: ptr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := model.Settings{DefaultClaudeModel: config.ClaudeModel, DefaultOpenAiModel: config.OpenAIModel, ClaudeCompat: noEndpoint, OpenAiCompat: noEndpoint, AiProvider: model.ProviderChatGPT, Background: model.BackgroundGrain, LargeFileMinMb: 250}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "settings.json" {
		t.Fatalf("folder holds %v (a temp file was left behind?)", entries)
	}
	fi, _ := os.Stat(filepath.Join(dir, "settings.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	// a new store reads it back
	again := &Store{Dir: s.Dir}
	if again.Get() != want {
		t.Fatalf("reloaded = %+v", again.Get())
	}
	// a partial patch leaves the rest alone
	got, _ = s.Patch(model.SettingsPatch{Background: ptr(model.BackgroundDots)})
	if got.Background != model.BackgroundDots || got.LargeFileMinMb != 250 || got.AiProvider != model.ProviderChatGPT {
		t.Fatalf("got %+v", got)
	}
}

func TestPatchValidation(t *testing.T) {
	s, _, dir := newStore(t)
	tests := []struct {
		name  string
		patch model.SettingsPatch
	}{
		{"provider", model.SettingsPatch{AiProvider: ptr(model.AiProvider("gemini"))}},
		{"empty provider", model.SettingsPatch{AiProvider: ptr(model.AiProvider(""))}},
		{"background", model.SettingsPatch{Background: ptr(model.Background("neon"))}},
		{"too small", model.SettingsPatch{LargeFileMinMb: ptr(9)}},
		{"negative", model.SettingsPatch{LargeFileMinMb: ptr(-5)}},
		{"too large", model.SettingsPatch{LargeFileMinMb: ptr(100_001)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Patch(tc.patch); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err == nil {
		t.Error("an invalid patch wrote the file")
	}
	// the edges are fine
	for _, mb := range []int{10, 100_000} {
		if _, err := s.Patch(model.SettingsPatch{LargeFileMinMb: ptr(mb)}); err != nil {
			t.Errorf("%d MB: %v", mb, err)
		}
	}
}

func TestBadFileFallsBackToDefaults(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":      "not json at all",
		"empty":        "",
		"array":        "[1,2,3]",
		"wrong types":  `{"aiProvider": 5, "background": true, "largeFileMinMb": "big", "confirmBeforeClean": "yes"}`,
		"out of range": `{"aiProvider": "gemini", "background": "neon", "largeFileMinMb": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, dir := newStore(t)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600)
			if got := s.Get(); got != (model.Settings{DefaultClaudeModel: config.ClaudeModel, DefaultOpenAiModel: config.OpenAIModel, ClaudeCompat: noEndpoint, OpenAiCompat: noEndpoint, AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}) {
				t.Fatalf("got %+v", got)
			}
		})
	}
	// one good field survives next to bad ones
	s, _, dir := newStore(t)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"aiProvider":"chatgpt","background":"neon","hasClaudeKey":true,"confirmBeforeClean":false}`), 0o600)
	got := s.Get()
	if got.AiProvider != model.ProviderChatGPT || got.Background != model.BackgroundMist || got.ConfirmBeforeClean || got.HasClaudeKey {
		t.Fatalf("got %+v", got)
	}
}

func TestKeysLiveInTheKeychainNotInTheFile(t *testing.T) {
	s, sec, dir := newStore(t)
	got, err := s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl)
	if err != nil || !got.HasClaudeKey || got.HasOpenAiKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	if string(sec.m[config.ClaudeKeyName]) != goodKey {
		t.Fatal("key not in the Keychain")
	}
	s.Patch(model.SettingsPatch{Background: ptr(model.BackgroundGrid)})
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if strings.Contains(string(b), "sk-ant") || strings.Contains(strings.ToLower(string(b)), "key") {
		t.Fatalf("settings.json mentions a key: %s", b)
	}
	if k, _ := s.Key(model.ProviderClaude); k != goodKey {
		t.Errorf("Key = %q", k)
	}
	if k, _ := s.Key(model.ProviderChatGPT); k != "" {
		t.Errorf("OpenAI key = %q", k)
	}

	// a fresh store learns it from the Keychain
	if !(&Store{Dir: s.Dir, Secrets: sec}).Get().HasClaudeKey {
		t.Error("presence not read from the Keychain")
	}

	// whitespace is trimmed, blank removes
	got, err = s.SetKey(model.ProviderChatGPT, ptr("  "+goodKey+"\n"), s.Endpoint(model.ProviderChatGPT).BaseUrl)
	if err != nil || !got.HasOpenAiKey || string(sec.m[config.OpenAIKeyName]) != goodKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	for _, blank := range []*string{nil, ptr(""), ptr("   ")} {
		got, err = s.SetKey(model.ProviderClaude, blank, s.Endpoint(model.ProviderClaude).BaseUrl)
		if err != nil || got.HasClaudeKey {
			t.Fatalf("got %+v err %v", got, err)
		}
		s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl)
	}
	s.SetKey(model.ProviderClaude, nil, s.Endpoint(model.ProviderClaude).BaseUrl)
	if _, ok := sec.m[config.ClaudeKeyName]; ok {
		t.Error("key not deleted")
	}
	if _, err := s.SetKey(model.ProviderClaude, nil, s.Endpoint(model.ProviderClaude).BaseUrl); err != nil {
		t.Errorf("removing a missing key: %v", err)
	}
}

func TestSetKeyValidationAndFailures(t *testing.T) {
	s, sec, _ := newStore(t)
	for _, bad := range []string{"short", strings.Repeat("a", 513), "has space inside the key 1234567890", "tab\tinside-the-key-1234567890"} {
		if _, err := s.SetKey(model.ProviderClaude, ptr(bad), s.Endpoint(model.ProviderClaude).BaseUrl); err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("SetKey(%q) err = %v", bad, err)
		}
	}
	if _, err := s.SetKey(model.AiProvider("x"), ptr(goodKey), ""); err == nil {
		t.Error("unknown provider accepted")
	}
	sec.setErr = errors.ErrUnsupported
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl); err == nil || !strings.Contains(err.Error(), "Secure storage is not available") {
		t.Errorf("err = %v", err)
	}
	sec.setErr = errors.New("user denied access to " + goodKey)
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl); err == nil || strings.Contains(err.Error(), goodKey) {
		t.Errorf("a Keychain error must not echo the key: %v", err)
	}
	if s.Get().HasClaudeKey {
		t.Error("failed save reported as stored")
	}
	if _, err := (&Store{Dir: s.Dir}).SetKey(model.ProviderClaude, ptr(goodKey), ""); err == nil {
		t.Error("no Keychain should fail")
	}
}

func TestKeychainErrorIsAskedOnceNotOnEveryGet(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.m = map[string][]byte{config.ClaudeKeyName: []byte(goodKey)}
	sec.getErr = errors.New("user denied")
	for range 5 {
		if s.Get().HasClaudeKey {
			t.Fatal("reported a key while the Keychain was denied")
		}
	}
	if sec.gets > 4 { // once per provider
		t.Fatalf("Keychain was read %d times for 5 Gets, a denied prompt would repeat", sec.gets)
	}
	// Key still reads on demand and reports the failure.
	if _, err := s.Key(model.ProviderClaude); err == nil {
		t.Error("Key should report the failure")
	}
	// Saving a key updates the cache.
	sec.getErr = nil
	got, err := s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl)
	if err != nil || !got.HasClaudeKey || !s.Get().HasClaudeKey {
		t.Fatalf("after SetKey: %+v err=%v", got, err)
	}
}

func TestKeychainIsNeverCalledWithTheMutexHeld(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.onCall = func() {
		if !s.mu.TryLock() {
			t.Error("s.mu is held across a Keychain call")
			return
		}
		s.mu.Unlock()
	}
	s.Get()
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetKey(model.ProviderClaude, nil, s.Endpoint(model.ProviderClaude).BaseUrl); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patch(model.SettingsPatch{ConfirmBeforeClean: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if sec.gets == 0 {
		t.Fatal("the Keychain was never read")
	}
}

func TestDefinitiveAnswersAreCached(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.m = map[string][]byte{config.ClaudeKeyName: []byte(goodKey)}
	if !s.Get().HasClaudeKey {
		t.Fatal("key not seen")
	}
	before := sec.gets
	s.Get()
	s.Get()
	if sec.gets != before {
		t.Error("definite answers should be cached")
	}
}

func TestConcurrentPatches(t *testing.T) {
	s, _, dir := newStore(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Patch(model.SettingsPatch{LargeFileMinMb: ptr(10 + i)})
			s.Get()
		}()
	}
	wg.Wait()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("folder holds %v", entries)
	}
	if mb := (&Store{Dir: s.Dir}).Get().LargeFileMinMb; mb < 10 || mb > 29 {
		t.Fatalf("file holds %d", mb)
	}
}

func TestDirFailure(t *testing.T) {
	s := &Store{Dir: func() (string, error) { return "", errors.New("no folder") }}
	if got := s.Get(); got.LargeFileMinMb != 500 {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.Patch(model.SettingsPatch{ConfirmBeforeClean: ptr(false)}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestKeyUpdatesThePresenceCache(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.m = map[string][]byte{config.ClaudeKeyName: []byte(goodKey)}
	sec.getErr = errors.New("user denied")
	if s.Get().HasClaudeKey {
		t.Fatal("key reported while denied")
	}
	sec.getErr = nil // approved later, when the assistant asks
	if k, err := s.Key(model.ProviderClaude); err != nil || k != goodKey {
		t.Fatalf("Key = %q, %v", k, err)
	}
	before := sec.gets
	if !s.Get().HasClaudeKey {
		t.Fatal("Settings still says no key after Key() read it")
	}
	if sec.gets != before {
		t.Error("Get should answer from the cache")
	}
	// not found is remembered too
	delete(sec.m, config.OpenAIKeyName)
	if k, err := s.Key(model.ProviderChatGPT); err != nil || k != "" {
		t.Fatalf("Key = %q, %v", k, err)
	}
	before = sec.gets
	s.Get()
	if sec.gets != before {
		t.Error("not-found from Key() should be cached")
	}
}

func TestKeySerialisesKeychainCalls(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.m = map[string][]byte{config.ClaudeKeyName: []byte(goodKey)}
	sec.onCall = func() {
		if s.keyMu.TryLock() {
			s.keyMu.Unlock()
			t.Error("Key() called the Keychain without keyMu")
		}
		if !s.mu.TryLock() {
			t.Error("s.mu held across a Keychain call")
			return
		}
		s.mu.Unlock()
	}
	if _, err := s.Key(model.ProviderClaude); err != nil {
		t.Fatal(err)
	}
}

const (
	hostA = "https://api.example.com/v1"
	hostB = "https://other.example.org/v1"
)

func setEndpoint(t *testing.T, s *Store, p model.AiProvider, base, mdl *string) (model.Settings, error) {
	t.Helper()
	patch := &model.AiEndpointPatch{BaseUrl: base, Model: mdl}
	if p == model.ProviderClaudeCompat {
		return s.Patch(model.SettingsPatch{ClaudeCompat: patch})
	}
	return s.Patch(model.SettingsPatch{OpenAiCompat: patch})
}

func TestCompatibleEndpointDefaultsAndSave(t *testing.T) {
	s, _, dir := newStore(t)
	got := s.Get()
	if got.ClaudeCompat != noEndpoint || got.OpenAiCompat != noEndpoint {
		t.Fatalf("defaults = %+v", got)
	}
	got, err := setEndpoint(t, s, model.ProviderOpenAICompat, ptr("  HTTPS://API.Example.com/v1/  "), ptr(" gpt-x "))
	if err != nil || got.OpenAiCompat.BaseUrl != hostA || got.OpenAiCompat.Model != "gpt-x" || got.ClaudeCompat != noEndpoint {
		t.Fatalf("got %+v err %v", got, err)
	}
	if e := s.Endpoint(model.ProviderOpenAICompat); e.BaseUrl != hostA || e.Model != "gpt-x" || e.HasKey {
		t.Errorf("Endpoint = %+v", e)
	}
	if e := s.Endpoint(model.ProviderClaude); e != (model.AiEndpoint{}) {
		t.Errorf("an official provider has no endpoint: %+v", e)
	}
	// persisted, in a fresh store
	again := &Store{Dir: s.Dir, Secrets: &memSecrets{}}
	if e := again.Get().OpenAiCompat; e.BaseUrl != hostA || e.Model != "gpt-x" {
		t.Errorf("not persisted: %+v", e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if !strings.Contains(string(b), `"openAiCompat"`) {
		t.Errorf("file = %s", b)
	}
	// provider can be chosen
	p := model.ProviderClaudeCompat
	if got, err := s.Patch(model.SettingsPatch{AiProvider: &p}); err != nil || got.AiProvider != p || s.Provider() != p {
		t.Fatalf("got %+v err %v", got, err)
	}
	// a patch without endpoint fields leaves them alone; empty strings clear
	if got, _ := s.Patch(model.SettingsPatch{Background: ptr(model.BackgroundGrid)}); got.OpenAiCompat.BaseUrl != hostA {
		t.Errorf("endpoint lost: %+v", got)
	}
	got, err = setEndpoint(t, s, model.ProviderOpenAICompat, nil, ptr(" "))
	if err != nil || got.OpenAiCompat.Model != "" || got.OpenAiCompat.BaseUrl != hostA {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestCompatiblePatchValidation(t *testing.T) {
	s, _, _ := newStore(t)
	for _, bad := range []string{
		"http://192.168.1.5", "http://example.com", "http://localhost.evil.com", "ftp://x.example", "https://u:p@x.example",
		"https://x.example/v1?a=b", "https://x.example/#f", "https://169.254.169.254", "https://0.0.0.0", "x.example", "https://x.example/ v1",
		strings.Repeat("a", 2049),
	} {
		if _, err := setEndpoint(t, s, model.ProviderClaudeCompat, ptr(bad), nil); err == nil {
			t.Errorf("accepted address %q", bad)
		}
	}
	for _, bad := range []string{strings.Repeat("m", 129), "bad\nname", "bad\x00"} {
		if _, err := setEndpoint(t, s, model.ProviderClaudeCompat, nil, ptr(bad)); err == nil {
			t.Errorf("accepted model %q", bad)
		}
	}
	if got := s.Get(); got.ClaudeCompat != noEndpoint {
		t.Errorf("a refused patch changed something: %+v", got)
	}
	// a refused field refuses the whole patch
	if _, err := s.Patch(model.SettingsPatch{LargeFileMinMb: ptr(800), ClaudeCompat: &model.AiEndpointPatch{BaseUrl: ptr("http://10.0.0.1")}}); err == nil || s.Get().LargeFileMinMb == 800 {
		t.Errorf("partial apply: %v", err)
	}
	for _, ok := range []string{"http://localhost:11434/v1", "http://127.0.0.1:1234", "http://[::1]:8080", "https://192.168.1.5/v1", "https://openrouter.ai/api/v1"} {
		if _, err := setEndpoint(t, s, model.ProviderOpenAICompat, ptr(ok), nil); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
}

func TestSanitizeCompatibleEndpoints(t *testing.T) {
	s, _, dir := newStore(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := `{"aiProvider":"openai-compatible",
		"claudeCompat":{"baseUrl":"http://192.168.1.5/v1","model":"bad\u0000name"},
		"openAiCompat":{"baseUrl":"HTTPS://Api.Example.com/v1//","model":"  ok-model "}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.AiProvider != model.ProviderOpenAICompat {
		t.Errorf("provider = %q", got.AiProvider)
	}
	if got.ClaudeCompat != noEndpoint {
		t.Errorf("invalid stored values survived: %+v", got.ClaudeCompat)
	}
	if got.OpenAiCompat.BaseUrl != "https://api.example.com/v1" || got.OpenAiCompat.Model != "ok-model" {
		t.Errorf("openAiCompat = %+v", got.OpenAiCompat)
	}
	for name, raw := range map[string]string{
		"old file":   `{"aiProvider":"claude","largeFileMinMb":300}`,
		"wrong type": `{"claudeCompat":"https://x.example","openAiCompat":[1]}`,
		"null":       `{"claudeCompat":null}`,
		"link local": `{"claudeCompat":{"baseUrl":"https://169.254.169.254","model":"m"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		fresh := &Store{Dir: s.Dir}
		if g := fresh.Get(); g.ClaudeCompat.BaseUrl != "" || g.OpenAiCompat.BaseUrl != "" {
			t.Errorf("%s: %+v", name, g)
		}
	}
}

func TestHostChangeDeletesTheKey(t *testing.T) {
	const key = "short-key"
	for _, p := range []model.AiProvider{model.ProviderClaudeCompat, model.ProviderOpenAICompat} {
		name := keyName(p)
		t.Run(string(p), func(t *testing.T) {
			s, sec, _ := newStore(t)
			if _, err := setEndpoint(t, s, p, ptr(hostA), ptr("m")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl); err != nil {
				t.Fatal(err)
			}
			has := func() bool {
				if p == model.ProviderClaudeCompat {
					return s.Get().ClaudeCompat.HasKey
				}
				return s.Get().OpenAiCompat.HasKey
			}
			if !has() || plainKey(sec, p) != key {
				t.Fatal("key not stored")
			}
			// same host, other path/model/case: key stays
			for _, u := range []string{"https://API.example.com/v2", "https://api.example.com", "https://api.example.com:443/x"} {
				if _, err := setEndpoint(t, s, p, ptr(u), ptr("m2")); err != nil {
					t.Fatal(err)
				}
				if !has() || sec.m[name] == nil {
					t.Fatalf("key deleted for the same host: %s", u)
				}
			}
			// another port is another service
			if _, err := setEndpoint(t, s, p, ptr("https://api.example.com:8443/v2"), nil); err != nil {
				t.Fatal(err)
			}
			if has() || sec.m[name] != nil {
				t.Fatal("key survived a port change")
			}
			// set again, then other host
			s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
			got, err := setEndpoint(t, s, p, ptr(hostB), nil)
			if err != nil || has() || sec.m[name] != nil {
				t.Fatalf("key survived a host change: %+v %v", got, err)
			}
			ep := got.OpenAiCompat
			if p == model.ProviderClaudeCompat {
				ep = got.ClaudeCompat
			}
			if ep.BaseUrl != hostB {
				t.Errorf("address not saved: %+v", ep)
			}
			// clearing the address also drops the key
			s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
			if _, err := setEndpoint(t, s, p, ptr(""), nil); err != nil || has() || sec.m[name] != nil {
				t.Fatalf("key survived clearing the address: %v", err)
			}
			// first address after a key was typed: nothing to protect yet
			s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
			if _, err := setEndpoint(t, s, p, ptr(hostA), nil); err != nil || !has() || sec.m[name] == nil {
				t.Fatalf("key deleted when the first address was set: %v", err)
			}
		})
	}
}

func TestHostChangeLeavesOtherKeysAlone(t *testing.T) {
	s, sec, _ := newStore(t)
	setEndpoint(t, s, model.ProviderClaudeCompat, ptr(hostA), nil)
	setEndpoint(t, s, model.ProviderOpenAICompat, ptr(hostA), nil)
	s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl)
	s.SetKey(model.ProviderChatGPT, ptr(goodKey), s.Endpoint(model.ProviderChatGPT).BaseUrl)
	s.SetKey(model.ProviderClaudeCompat, ptr("k1"), s.Endpoint(model.ProviderClaudeCompat).BaseUrl)
	s.SetKey(model.ProviderOpenAICompat, ptr("k2"), s.Endpoint(model.ProviderOpenAICompat).BaseUrl)
	got, err := setEndpoint(t, s, model.ProviderClaudeCompat, ptr(hostB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaudeCompat.HasKey || !got.OpenAiCompat.HasKey || !got.HasClaudeKey || !got.HasOpenAiKey {
		t.Fatalf("got %+v", got)
	}
	if sec.m[config.OpenAICompatKeyName] == nil || sec.m[config.ClaudeKeyName] == nil || sec.m[config.OpenAIKeyName] == nil {
		t.Fatal("another key was deleted")
	}
}

func TestHostChangeWithKeychainFailureChangesNothing(t *testing.T) {
	s, sec, _ := newStore(t)
	setEndpoint(t, s, model.ProviderOpenAICompat, ptr(hostA), ptr("m"))
	s.SetKey(model.ProviderOpenAICompat, ptr("k"), s.Endpoint(model.ProviderOpenAICompat).BaseUrl)
	sec.delErr = errors.New("denied")
	_, err := setEndpoint(t, s, model.ProviderOpenAICompat, ptr(hostB), ptr("m2"))
	if err == nil || strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
	got := s.Get().OpenAiCompat
	if got.BaseUrl != hostA || got.Model != "m" || !got.HasKey {
		t.Fatalf("state changed after a failed deletion: %+v", got)
	}
	b, _ := os.ReadFile(func() string { f, _ := s.file(); return f }())
	if strings.Contains(string(b), "other.example.org") {
		t.Fatal("new address reached the file")
	}
	// a path change moves the key, so it needs the Keychain too and changes nothing when that fails
	if _, err := setEndpoint(t, s, model.ProviderOpenAICompat, ptr(hostA+"/x"), nil); err == nil || s.Get().OpenAiCompat.BaseUrl != hostA {
		t.Fatalf("path change with a failing Keychain: %v", err)
	}
	sec.delErr = nil
	if _, err := setEndpoint(t, s, model.ProviderOpenAICompat, ptr(hostA+"/x"), nil); err != nil || !s.Get().OpenAiCompat.HasKey {
		t.Fatal(err)
	}
}

func TestPresenceCacheForCompatibleKeys(t *testing.T) {
	s, sec, _ := newStore(t)
	sec.m = map[string][]byte{config.ClaudeCompatKeyName: encodeBound("", "abc")}
	setEndpoint(t, s, model.ProviderClaudeCompat, ptr(hostA), nil)
	if !s.Get().ClaudeCompat.HasKey || s.Get().OpenAiCompat.HasKey {
		t.Fatal("presence wrong")
	}
	gets := sec.gets
	for range 5 {
		s.Get()
	}
	if sec.gets != gets {
		t.Errorf("Keychain read %d more times", sec.gets-gets)
	}
	setEndpoint(t, s, model.ProviderClaudeCompat, ptr(hostB), nil)
	if s.Get().ClaudeCompat.HasKey {
		t.Error("the cache still says there is a key")
	}
	if k, err := s.Key(model.ProviderClaudeCompat); err != nil || k != "" {
		t.Errorf("Key = %q %v", k, err)
	}
}

func TestCompatibleKeyValidation(t *testing.T) {
	for _, p := range []model.AiProvider{model.ProviderClaudeCompat, model.ProviderOpenAICompat} {
		s, sec, _ := newStore(t)
		for _, good := range []string{"x", "ollama", "sk-or-v1-abc", strings.Repeat("k", 512), "  padded  "} {
			if _, err := s.SetKey(p, ptr(good), s.Endpoint(p).BaseUrl); err != nil {
				t.Errorf("%s: rejected %q: %v", p, good, err)
			}
		}
		if plainKey(sec, p) != "padded" {
			t.Errorf("stored %q", sec.m[keyName(p)])
		}
		for _, bad := range []string{strings.Repeat("k", 513), "has space", "tab\there", "new\nline", "ctl\x01", "ünicode", "nul\x00"} {
			if _, err := s.SetKey(p, ptr(bad), s.Endpoint(p).BaseUrl); err == nil || strings.Contains(err.Error(), bad) {
				t.Errorf("%s: accepted or echoed %q: %v", p, bad, err)
			}
		}
		if got, _ := s.SetKey(p, ptr("  "), s.Endpoint(p).BaseUrl); got.ClaudeCompat.HasKey || got.OpenAiCompat.HasKey || sec.m[keyName(p)] != nil {
			t.Error("a blank key should remove the stored one")
		}
	}
	// the official rules are unchanged
	s, _, _ := newStore(t)
	if _, err := s.SetKey(model.ProviderClaude, ptr("short"), s.Endpoint(model.ProviderClaude).BaseUrl); err == nil {
		t.Error("an official key under 20 characters was accepted")
	}
}

func TestKeyNames(t *testing.T) {
	want := map[model.AiProvider]string{
		model.ProviderClaude: "claude-api-key", model.ProviderChatGPT: "openai-api-key",
		model.ProviderClaudeCompat: "claude-compatible-api-key", model.ProviderOpenAICompat: "openai-compatible-api-key",
	}
	for p, name := range want {
		if keyName(p) != name || !validProvider(p) {
			t.Errorf("%s: %s", p, keyName(p))
		}
	}
	if validProvider("other") {
		t.Error("unknown provider valid")
	}
}

func TestConcurrentPatchesAreSafe(t *testing.T) {
	s, _, _ := newStore(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u := hostA
			if i%2 == 0 {
				u = hostB
			}
			s.Patch(model.SettingsPatch{OpenAiCompat: &model.AiEndpointPatch{BaseUrl: &u}})
			s.Get()
		}()
	}
	wg.Wait()
}

// plainKey is the key inside a compatible provider's Keychain entry.
func plainKey(sec *memSecrets, p model.AiProvider) string {
	bk, _ := decodeBound(sec.m[keyName(p)])
	return bk.Key
}

func authorityOf(t *testing.T, raw string) string {
	t.Helper()
	e, err := endpoint.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e.Binding()
}

func TestKeyIsBoundToItsAuthority(t *testing.T) {
	const key = "secret-key-1"
	p := model.ProviderOpenAICompat
	t.Run("stored with the authority", func(t *testing.T) {
		s, sec, _ := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		bk, ok := decodeBound(sec.m[keyName(p)])
		if !ok || bk.Address != authorityOf(t, hostA) || bk.Key != key {
			t.Fatalf("entry = %+v", bk)
		}
		if got, err := s.Key(p); err != nil || got != key {
			t.Fatalf("Key = %q %v", got, err)
		}
		if got, err := s.KeyFor(p, authorityOf(t, hostA)); err != nil || got != key {
			t.Fatalf("KeyFor = %q %v", got, err)
		}
		for _, other := range []string{authorityOf(t, hostB), "", "api.example.com:8443"} {
			if got, err := s.KeyFor(p, other); err == nil || got != "" {
				t.Errorf("KeyFor(%q) = %q %v", other, got, err)
			}
		}
	})
	t.Run("scenario A: settings.json edited behind our back", func(t *testing.T) {
		s, sec, dir := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		file := filepath.Join(dir, "settings.json")
		b, _ := os.ReadFile(file)
		edited := strings.ReplaceAll(string(b), "api.example.com", "evil.example.net")
		if edited == string(b) {
			t.Fatal("edit did not apply")
		}
		os.WriteFile(file, []byte(edited), 0o600)
		restarted := &Store{Dir: s.Dir, Secrets: sec} // a new session reads the file
		if ep := restarted.Endpoint(p); !strings.Contains(ep.BaseUrl, "evil.example.net") {
			t.Fatalf("setup: %+v", ep)
		}
		if got, err := restarted.Key(p); err == nil || got != "" {
			t.Fatalf("key released for an edited address: %q %v", got, err)
		}
		if got, err := restarted.KeyFor(p, authorityOf(t, "https://evil.example.net")); err == nil || got != "" {
			t.Fatalf("KeyFor released the key to the edited host: %q %v", got, err)
		}
		// and not for the address it was saved with either, since that is no longer the saved one
		if got, err := restarted.KeyFor(p, authorityOf(t, hostA)); err == nil || got != "" {
			t.Fatalf("KeyFor released the key although the saved address moved: %q %v", got, err)
		}
	})
	t.Run("scenario B: invalid stored address then a new host", func(t *testing.T) {
		s, sec, dir := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		file := filepath.Join(dir, "settings.json")
		b, _ := os.ReadFile(file)
		os.WriteFile(file, []byte(strings.ReplaceAll(string(b), hostA, "http://192.168.1.5/v1")), 0o600)
		restarted := &Store{Dir: s.Dir, Secrets: sec}
		if ep := restarted.Endpoint(p); ep.BaseUrl != "" {
			t.Fatalf("invalid address survived: %+v", ep)
		}
		if got, err := restarted.Key(p); err == nil || got != "" {
			t.Fatalf("key released with a blank address: %q %v", got, err)
		}
		// the next host saved must not inherit the key
		got, err := setEndpoint(t, restarted, p, ptr(hostB), nil)
		if err != nil || got.OpenAiCompat.HasKey || sec.m[keyName(p)] != nil {
			t.Fatalf("key inherited: %+v %v", got, err)
		}
		if k, err := restarted.KeyFor(p, authorityOf(t, hostB)); err != nil || k != "" {
			t.Fatalf("KeyFor = %q %v", k, err)
		}
	})
	t.Run("scenario B2: the old host comes back", func(t *testing.T) {
		s, sec, dir := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		file := filepath.Join(dir, "settings.json")
		b, _ := os.ReadFile(file)
		os.WriteFile(file, []byte(strings.ReplaceAll(string(b), hostA, "ftp://broken")), 0o600)
		restarted := &Store{Dir: s.Dir, Secrets: sec}
		restarted.Get()
		if _, err := setEndpoint(t, restarted, p, ptr(hostA), nil); err != nil {
			t.Fatal(err)
		}
		if k, err := restarted.Key(p); err != nil || k != key {
			t.Fatalf("a key for the same authority should still work: %q %v", k, err)
		}
	})
	t.Run("key first, then the first address binds it", func(t *testing.T) {
		s, sec, _ := newStore(t)
		got, err := s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		if err != nil || !got.OpenAiCompat.HasKey {
			t.Fatalf("%+v %v", got, err)
		}
		if bk, _ := decodeBound(sec.m[keyName(p)]); bk.Address != "" {
			t.Fatalf("bound too early: %+v", bk)
		}
		if k, err := s.Key(p); err == nil || k != "" {
			t.Fatalf("an unbound key was released: %q %v", k, err)
		}
		got, err = setEndpoint(t, s, p, ptr(hostA), nil)
		if err != nil || !got.OpenAiCompat.HasKey {
			t.Fatalf("%+v %v", got, err)
		}
		if bk, _ := decodeBound(sec.m[keyName(p)]); bk.Address != authorityOf(t, hostA) || bk.Key != key {
			t.Fatalf("not bound to the first address: %+v", bk)
		}
		if k, err := s.Key(p); err != nil || k != key {
			t.Fatalf("Key = %q %v", k, err)
		}
	})
	t.Run("an unbound key is not released to a file-edited address", func(t *testing.T) {
		s, sec, dir := newStore(t)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl) // saved before any address
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"openAiCompat":{"baseUrl":"https://evil.example.net/v1","model":"m"}}`), 0o600)
		restarted := &Store{Dir: s.Dir, Secrets: sec}
		if k, err := restarted.Key(p); err == nil || k != "" {
			t.Fatalf("released: %q %v", k, err)
		}
	})
	t.Run("legacy plain value is never released", func(t *testing.T) {
		s, sec, _ := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		sec.m = map[string][]byte{keyName(p): []byte(key)}
		if k, err := s.Key(p); err == nil || k != "" {
			t.Fatalf("released: %q %v", k, err)
		}
	})
	t.Run("race: A, then B with its own key, then the question for A", func(t *testing.T) {
		s, _, _ := newStore(t)
		setEndpoint(t, s, p, ptr(hostA), nil)
		s.SetKey(p, ptr("key-for-A"), s.Endpoint(p).BaseUrl)
		authA := authorityOf(t, hostA) // what the question in flight is about
		setEndpoint(t, s, p, ptr(hostB), nil)
		s.SetKey(p, ptr("key-for-B"), s.Endpoint(p).BaseUrl)
		// the in-flight call asks for the key of A: it must not get B's key
		if k, err := s.KeyFor(p, authA); err == nil || k != "" {
			t.Fatalf("B's key was released for A: %q %v", k, err)
		}
		// back to A: the key was wiped on the way, so there is nothing to leak
		setEndpoint(t, s, p, ptr(hostA), nil)
		if k, err := s.KeyFor(p, authA); err != nil || k != "" {
			t.Fatalf("KeyFor = %q %v", k, err)
		}
	})
	t.Run("official keys stay plain", func(t *testing.T) {
		s, sec, _ := newStore(t)
		s.SetKey(model.ProviderClaude, ptr(goodKey), s.Endpoint(model.ProviderClaude).BaseUrl)
		if string(sec.m[config.ClaudeKeyName]) != goodKey {
			t.Fatal("official key format changed")
		}
		if k, err := s.KeyFor(model.ProviderClaude, "ignored"); err != nil || k != goodKey {
			t.Fatalf("%q %v", k, err)
		}
	})
	t.Run("binding failure changes nothing", func(t *testing.T) {
		s, sec, _ := newStore(t)
		s.SetKey(p, ptr(key), s.Endpoint(p).BaseUrl)
		sec.setErr = errors.New("denied")
		if _, err := setEndpoint(t, s, p, ptr(hostA), nil); err == nil {
			t.Fatal("expected an error")
		}
		if s.Endpoint(p).BaseUrl != "" {
			t.Fatal("address saved although the key could not be bound")
		}
	})
}

// editFile rewrites settings.json behind the store's back and returns a new
// session on the same Keychain.
func editFile(t *testing.T, s *Store, sec *memSecrets, from, to string) *Store {
	t.Helper()
	f, _ := s.file()
	b, _ := os.ReadFile(f)
	edited := strings.ReplaceAll(string(b), from, to)
	if edited == string(b) {
		t.Fatal("edit did not apply")
	}
	if err := os.WriteFile(f, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Store{Dir: s.Dir, Secrets: sec}
}

func epOf(g model.Settings, p model.AiProvider) model.AiEndpoint {
	if p == model.ProviderClaudeCompat {
		return g.ClaudeCompat
	}
	return g.OpenAiCompat
}

func TestKeyStateValues(t *testing.T) {
	p := model.ProviderOpenAICompat
	s, sec, _ := newStore(t)
	if e := epOf(s.Get(), p); e.KeyState != model.KeyNone || e.HasKey || e.KeyHost != "" {
		t.Fatalf("empty: %+v", e)
	}
	setEndpoint(t, s, p, ptr(hostA), nil)
	if _, err := s.SetKey(p, ptr("k"), hostA); err != nil {
		t.Fatal(err)
	}
	if e := epOf(s.Get(), p); e.KeyState != model.KeyOK || !e.HasKey || e.KeyHost != "" {
		t.Fatalf("saved: %+v", e)
	}
	r := editFile(t, s, sec, "api.example.com", "evil.example.net")
	if e := epOf(r.Get(), p); e.KeyState != model.KeyOtherAddress || e.HasKey || e.KeyHost != "api.example.com" {
		t.Fatalf("file-edited host: %+v", e)
	}
	// no key stored at all stays "none" whatever the address
	sec.m = nil
	r = &Store{Dir: s.Dir, Secrets: sec}
	if e := epOf(r.Get(), p); e.KeyState != model.KeyNone {
		t.Fatalf("no key: %+v", e)
	}
}

func TestFileEditedHostNamesBothHosts(t *testing.T) {
	p := model.ProviderOpenAICompat
	s, sec, _ := newStore(t)
	setEndpoint(t, s, p, ptr(hostA+"/secret-path"), nil)
	s.SetKey(p, ptr("the-key"), hostA+"/secret-path")
	r := editFile(t, s, sec, "api.example.com", "evil.example.net")
	_, err := r.Key(p)
	if err == nil {
		t.Fatal("key released")
	}
	for _, want := range []string{"api.example.com", "evil.example.net"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
	for _, bad := range []string{"secret-path", "the-key", "https://"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("message leaks %q: %v", bad, err)
		}
	}
	if sec.m[keyName(p)] == nil {
		t.Error("loading an edited file must not delete the key")
	}
	// the page showing the OLD address cannot re-bind the key to the edited host
	if _, err := r.SetKey(p, ptr("new"), hostA); err == nil || !strings.Contains(err.Error(), "evil.example.net") {
		t.Fatalf("mismatched save: %v", err)
	}
	if plainKey(sec, p) != "the-key" {
		t.Error("a refused save changed the key")
	}
}

func TestFileEditedPathDoesNotReleaseTheKey(t *testing.T) {
	p := model.ProviderClaudeCompat
	s, sec, _ := newStore(t)
	setEndpoint(t, s, p, ptr("https://gw.example/v1/acct-a/gw"), nil)
	s.SetKey(p, ptr("k"), "https://gw.example/v1/acct-a/gw")
	r := editFile(t, s, sec, "acct-a", "acct-b")
	if k, err := r.Key(p); err == nil || k != "" || !strings.Contains(err.Error(), "gw.example") || !strings.Contains(err.Error(), "different address") {
		t.Fatalf("Key = %q %v", k, err)
	}
	if e := epOf(r.Get(), p); e.KeyState != model.KeyOtherAddress || e.HasKey || e.KeyHost != "gw.example" {
		t.Fatalf("state: %+v", e)
	}
	if plainKey(sec, p) != "k" {
		t.Error("the key was deleted by loading")
	}
	// a page path change on top of the edited file must not launder the key
	if _, err := setEndpoint(t, r, p, ptr("https://gw.example/v1/acct-c/gw"), nil); err != nil {
		t.Fatal(err)
	}
	if sec.m[keyName(p)] != nil {
		t.Error("a key bound to another path was carried to a new path")
	}
}

func TestPageSavedPathChangeRebindsTheKey(t *testing.T) {
	p := model.ProviderOpenAICompat
	a, b := "https://gw.example/v1/acct/one", "https://gw.example/v1/acct/two"
	s, sec, _ := newStore(t)
	setEndpoint(t, s, p, ptr(a), nil)
	s.SetKey(p, ptr("k"), a)
	sec.calls = nil
	got, err := setEndpoint(t, s, p, ptr(b+"/"), nil)
	if err != nil || got.OpenAiCompat.KeyState != model.KeyOK || !got.OpenAiCompat.HasKey {
		t.Fatalf("%+v %v", got.OpenAiCompat, err)
	}
	bk, _ := decodeBound(sec.m[keyName(p)])
	if bk.Address != "https://gw.example:443/v1/acct/two" || bk.Key != "k" {
		t.Fatalf("entry = %+v", bk)
	}
	if k, err := s.Key(p); err != nil || k != "k" {
		t.Fatalf("Key = %q %v", k, err)
	}
	if k, err := s.KeyFor(p, authorityOf(t, a)); err == nil || k != "" {
		t.Fatalf("the old path still gets the key: %q %v", k, err)
	}
	if strings.Join(sec.calls, ",") != "del:"+keyName(p)+",set:"+keyName(p) {
		t.Errorf("calls = %v", sec.calls)
	}
	// restart: the new binding is what is on disk
	if k, err := (&Store{Dir: s.Dir, Secrets: sec}).Key(p); err != nil || k != "k" {
		t.Fatalf("after restart: %q %v", k, err)
	}
}

func TestPageSavedSchemeOrPortChangeDeletesTheKey(t *testing.T) {
	p := model.ProviderOpenAICompat
	for name, to := range map[string]string{
		"scheme":            "https://localhost:8443/v1",
		"port":              "http://localhost:8444/v1",
		"host":              "http://127.0.0.1:8443/v1",
		"scheme to default": "https://localhost/v1",
	} {
		t.Run(name, func(t *testing.T) {
			s, sec, _ := newStore(t)
			setEndpoint(t, s, p, ptr("http://localhost:8443/v1"), nil)
			s.SetKey(p, ptr("k"), "http://localhost:8443/v1")
			got, err := setEndpoint(t, s, p, ptr(to), nil)
			if err != nil || got.OpenAiCompat.KeyState != model.KeyNone || sec.m[keyName(p)] != nil {
				t.Fatalf("%+v %v", got.OpenAiCompat, err)
			}
		})
	}
	t.Run("file-edited scheme is not released", func(t *testing.T) {
		s, sec, _ := newStore(t)
		setEndpoint(t, s, p, ptr("http://localhost:443/v1"), nil)
		s.SetKey(p, ptr("k"), "http://localhost:443/v1")
		r := editFile(t, s, sec, "http://localhost:443", "https://localhost")
		if k, err := r.Key(p); err == nil || k != "" {
			t.Fatalf("Key = %q %v", k, err)
		}
		if k, err := r.KeyFor(p, "https://localhost:443/v1"); err == nil || k != "" {
			t.Fatalf("KeyFor = %q %v", k, err)
		}
	})
}

func TestSetKeyRefusesAnAddressThatIsNotTheSavedOne(t *testing.T) {
	p := model.ProviderOpenAICompat
	s, sec, _ := newStore(t)
	// no address saved yet: a key with no address binds later; with an address on screen it is refused
	if _, err := s.SetKey(p, ptr("k"), hostA); err == nil || !strings.Contains(err.Error(), "Save the address first") {
		t.Fatalf("unsaved address: %v", err)
	}
	if len(sec.calls) != 0 {
		t.Fatalf("Keychain touched: %v", sec.calls)
	}
	setEndpoint(t, s, p, ptr(hostA), nil)
	for _, shown := range []string{hostB, "", hostA + "/other", "http://localhost:1234", "ftp://x"} {
		_, err := s.SetKey(p, ptr("k"), shown)
		if err == nil {
			t.Errorf("%q accepted", shown)
		}
	}
	if _, err := s.SetKey(p, ptr("k"), hostB); err == nil || !strings.Contains(err.Error(), "api.example.com") || strings.Contains(err.Error(), "other.example.org") {
		t.Errorf("message should name the saved host only: %v", err)
	}
	if len(sec.calls) != 0 {
		t.Fatalf("Keychain touched: %v", sec.calls)
	}
	// other spellings of the same address are the same address
	for _, shown := range []string{hostA, hostA + "/", "https://API.example.com:443/v1", "  " + hostA + "  "} {
		if _, err := s.SetKey(p, ptr("k"), shown); err != nil {
			t.Errorf("%q refused: %v", shown, err)
		}
	}
	// removing needs no address
	if got, err := s.SetKey(p, nil, "whatever"); err != nil || got.OpenAiCompat.KeyState != model.KeyNone {
		t.Fatalf("remove: %+v %v", got.OpenAiCompat, err)
	}
	// official providers ignore it
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey), "nonsense"); err != nil {
		t.Fatal(err)
	}
}

func TestSetKeyDeletesBeforeItAdds(t *testing.T) {
	for _, tc := range []struct {
		p   model.AiProvider
		key string
	}{{model.ProviderClaude, goodKey}, {model.ProviderChatGPT, goodKey}, {model.ProviderClaudeCompat, "k"}, {model.ProviderOpenAICompat, "k"}} {
		t.Run(string(tc.p), func(t *testing.T) {
			s, sec, _ := newStore(t)
			base := ""
			if tc.p.Compatible() {
				base = hostA
				setEndpoint(t, s, tc.p, ptr(hostA), nil)
			}
			sec.m = map[string][]byte{keyName(tc.p): []byte("planted by another app")}
			sec.calls = nil
			if _, err := s.SetKey(tc.p, ptr(tc.key), base); err != nil {
				t.Fatal(err)
			}
			if strings.Join(sec.calls, ",") != "del:"+keyName(tc.p)+",set:"+keyName(tc.p) {
				t.Errorf("calls = %v", sec.calls)
			}
			// a failed add leaves nothing behind and the page is told there is no key
			sec.setErr = errors.New("denied")
			if _, err := s.SetKey(tc.p, ptr(tc.key), base); err == nil {
				t.Fatal("expected an error")
			}
			if sec.m[keyName(tc.p)] != nil {
				t.Error("the old item survived a failed save")
			}
			if s.Get().HasClaudeKey && tc.p == model.ProviderClaude {
				t.Error("stale presence")
			}
		})
	}
	t.Run("a failed delete saves nothing", func(t *testing.T) {
		s, sec, _ := newStore(t)
		sec.delErr = errors.New("denied")
		if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey), ""); err == nil {
			t.Fatal("expected an error")
		}
		for _, c := range sec.calls {
			if strings.HasPrefix(c, "set:") {
				t.Errorf("added after a failed delete: %v", sec.calls)
			}
		}
	})
}

func TestVersion1EntriesAreNotTrusted(t *testing.T) {
	p := model.ProviderOpenAICompat
	s, sec, _ := newStore(t)
	setEndpoint(t, s, p, ptr(hostA), nil)
	v1 := []byte(`{"v":1,"authority":"api.example.com:443","key":"old-key"}`)
	sec.m = map[string][]byte{keyName(p): v1}
	r := &Store{Dir: s.Dir, Secrets: sec}
	if k, err := r.Key(p); err == nil || k != "" || !strings.Contains(err.Error(), "older version") {
		t.Fatalf("Key = %q %v", k, err)
	}
	if e := epOf(r.Get(), p); e.KeyState != model.KeyOtherAddress || e.HasKey {
		t.Fatalf("state: %+v", e)
	}
	if string(sec.m[keyName(p)]) != string(v1) {
		t.Error("loading changed the entry")
	}
	// entering the key once more gives a version 2 entry that works
	if _, err := r.SetKey(p, ptr("new-key"), hostA); err != nil {
		t.Fatal(err)
	}
	if k, err := r.Key(p); err != nil || k != "new-key" {
		t.Fatalf("Key = %q %v", k, err)
	}
	if bk, ok := decodeBound(sec.m[keyName(p)]); !ok || bk.V != 2 || bk.Address != authorityOf(t, hostA) {
		t.Fatalf("entry = %+v", bk)
	}
}

func TestOfficialModelChoice(t *testing.T) {
	s, _, dir := newStore(t)
	g := s.Get()
	if g.ClaudeModel != "" || g.OpenAiModel != "" || g.DefaultClaudeModel != config.ClaudeModel || g.DefaultOpenAiModel != config.OpenAIModel {
		t.Fatalf("defaults: %+v", g)
	}
	got, err := s.Patch(model.SettingsPatch{ClaudeModel: ptr("  claude-custom-1 "), OpenAiModel: ptr("gpt-custom")})
	if err != nil || got.ClaudeModel != "claude-custom-1" || got.OpenAiModel != "gpt-custom" {
		t.Fatalf("%+v %v", got, err)
	}
	if s.OfficialModel(model.ProviderClaude) != "claude-custom-1" || s.OfficialModel(model.ProviderChatGPT) != "gpt-custom" || s.OfficialModel(model.ProviderOpenAICompat) != "" {
		t.Error("OfficialModel wrong")
	}
	if again := (&Store{Dir: func() (string, error) { return dir, nil }, Secrets: &memSecrets{}}).Get(); again.ClaudeModel != "claude-custom-1" || again.OpenAiModel != "gpt-custom" {
		t.Fatalf("not persisted: %+v", again)
	}
	// a patch that leaves the field out keeps it; an empty one resets it to the default
	if got, _ := s.Patch(model.SettingsPatch{Background: ptr(model.BackgroundGrid)}); got.ClaudeModel != "claude-custom-1" {
		t.Error("an unrelated patch changed the model")
	}
	if got, err := s.Patch(model.SettingsPatch{ClaudeModel: ptr("  ")}); err != nil || got.ClaudeModel != "" || got.OpenAiModel != "gpt-custom" {
		t.Fatalf("%+v %v", got, err)
	}
	// the same rules as the compatible editor's model field
	for _, bad := range []string{strings.Repeat("a", 129), "bad\nname", "evil\u202ename", "nb\u00a0sp", "x\u3164y", "line\u2028sep"} {
		before := s.Get()
		if _, err := s.Patch(model.SettingsPatch{OpenAiModel: ptr(bad), Background: ptr(model.BackgroundDots)}); err == nil {
			t.Errorf("%q accepted", bad)
		}
		if s.Get() != before {
			t.Errorf("%q: a refused patch changed something", bad)
		}
	}
	// a bad value in the file falls back to the default
	f, _ := s.file()
	os.WriteFile(f, []byte(`{"claudeModel":"a\u202eb","openAiModel":5}`), 0o600)
	r := &Store{Dir: s.Dir, Secrets: &memSecrets{}}
	if g := r.Get(); g.ClaudeModel != "" || g.OpenAiModel != "" {
		t.Fatalf("bad stored model survived: %+v", g)
	}
}
