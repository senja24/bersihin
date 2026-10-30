package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

type memSecrets struct {
	mu     sync.Mutex
	m      map[string][]byte
	gets   int
	setErr error
	getErr error
	delErr error
	onCall func() // runs inside every call, before the answer
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

func ptr[T any](v T) *T { return &v }

const goodKey = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"

func TestDefaults(t *testing.T) {
	s, _, dir := newStore(t)
	got := s.Get()
	want := model.Settings{AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}
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
	want := model.Settings{AiProvider: model.ProviderChatGPT, Background: model.BackgroundGrain, LargeFileMinMb: 250}
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
			if got := s.Get(); got != (model.Settings{AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}) {
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
	got, err := s.SetKey(model.ProviderClaude, ptr(goodKey))
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
	got, err = s.SetKey(model.ProviderChatGPT, ptr("  "+goodKey+"\n"))
	if err != nil || !got.HasOpenAiKey || string(sec.m[config.OpenAIKeyName]) != goodKey {
		t.Fatalf("got %+v err %v", got, err)
	}
	for _, blank := range []*string{nil, ptr(""), ptr("   ")} {
		got, err = s.SetKey(model.ProviderClaude, blank)
		if err != nil || got.HasClaudeKey {
			t.Fatalf("got %+v err %v", got, err)
		}
		s.SetKey(model.ProviderClaude, ptr(goodKey))
	}
	s.SetKey(model.ProviderClaude, nil)
	if _, ok := sec.m[config.ClaudeKeyName]; ok {
		t.Error("key not deleted")
	}
	if _, err := s.SetKey(model.ProviderClaude, nil); err != nil {
		t.Errorf("removing a missing key: %v", err)
	}
}

func TestSetKeyValidationAndFailures(t *testing.T) {
	s, sec, _ := newStore(t)
	for _, bad := range []string{"short", strings.Repeat("a", 513), "has space inside the key 1234567890", "tab\tinside-the-key-1234567890"} {
		if _, err := s.SetKey(model.ProviderClaude, ptr(bad)); err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("SetKey(%q) err = %v", bad, err)
		}
	}
	if _, err := s.SetKey(model.AiProvider("x"), ptr(goodKey)); err == nil {
		t.Error("unknown provider accepted")
	}
	sec.setErr = errors.ErrUnsupported
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey)); err == nil || !strings.Contains(err.Error(), "Secure storage is not available") {
		t.Errorf("err = %v", err)
	}
	sec.setErr = errors.New("user denied access to " + goodKey)
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey)); err == nil || strings.Contains(err.Error(), goodKey) {
		t.Errorf("a Keychain error must not echo the key: %v", err)
	}
	if s.Get().HasClaudeKey {
		t.Error("failed save reported as stored")
	}
	if _, err := (&Store{Dir: s.Dir}).SetKey(model.ProviderClaude, ptr(goodKey)); err == nil {
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
	if sec.gets > 2 { // once per provider
		t.Fatalf("Keychain was read %d times for 5 Gets, a denied prompt would repeat", sec.gets)
	}
	// Key still reads on demand and reports the failure.
	if _, err := s.Key(model.ProviderClaude); err == nil {
		t.Error("Key should report the failure")
	}
	// Saving a key updates the cache.
	sec.getErr = nil
	got, err := s.SetKey(model.ProviderClaude, ptr(goodKey))
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
	if _, err := s.SetKey(model.ProviderClaude, ptr(goodKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetKey(model.ProviderClaude, nil); err != nil {
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
