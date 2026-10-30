// Package settings keeps the user's preferences in a JSON file in MyGo's user
// data folder (atomic writes) and the API keys in the Keychain. A key is
// never written to the file, logged or sent to the page: the page learns only
// whether one is stored.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

// ErrNotFound is what Secrets.Get returns when nothing is stored under a key.
var ErrNotFound = errors.New("secret not found")

// Secrets is the Keychain (mygo.Secrets).
type Secrets interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

const (
	minLargeMb = 10
	maxLargeMb = 100_000
	fileName   = "settings.json"
)

type stored struct {
	AiProvider         model.AiProvider `json:"aiProvider"`
	Background         model.Background `json:"background"`
	LargeFileMinMb     int              `json:"largeFileMinMb"`
	ConfirmBeforeClean bool             `json:"confirmBeforeClean"`
}

func defaults() stored {
	return stored{AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}
}

func validProvider(p model.AiProvider) bool {
	return p == model.ProviderClaude || p == model.ProviderChatGPT
}

func validBackground(b model.Background) bool {
	switch b {
	case model.BackgroundMist, model.BackgroundGrid, model.BackgroundDots, model.BackgroundGrain:
		return true
	}
	return false
}

// Store reads and writes the settings.
type Store struct {
	// Dir returns the folder that holds settings.json (MyGo's user data folder).
	Dir     func() (string, error)
	Secrets Secrets

	// keyMu serialises Keychain calls. mu guards the fields below and is never
	// held across a Keychain call (a prompt can stay up for a long time).
	keyMu   sync.Mutex
	mu      sync.Mutex
	cached  *stored
	present map[model.AiProvider]bool // what the Keychain said once; kept for the session
}

func keyName(p model.AiProvider) string {
	if p == model.ProviderChatGPT {
		return config.OpenAIKeyName
	}
	return config.ClaudeKeyName
}

func (s *Store) file() (string, error) {
	dir, err := s.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// sanitize takes what is on disk leniently: a bad field falls back to its default.
func sanitize(b []byte) stored {
	out := defaults()
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	var p model.AiProvider
	if json.Unmarshal(raw["aiProvider"], &p) == nil && validProvider(p) {
		out.AiProvider = p
	}
	var bg model.Background
	if json.Unmarshal(raw["background"], &bg) == nil && validBackground(bg) {
		out.Background = bg
	}
	var mb float64
	if json.Unmarshal(raw["largeFileMinMb"], &mb) == nil && mb >= minLargeMb && mb <= maxLargeMb {
		out.LargeFileMinMb = int(mb + 0.5)
	}
	var c bool
	if json.Unmarshal(raw["confirmBeforeClean"], &c) == nil {
		out.ConfirmBeforeClean = c
	}
	return out
}

// load must be called with s.mu held.
func (s *Store) load() stored {
	if s.cached != nil {
		return *s.cached
	}
	cur := defaults()
	if f, err := s.file(); err == nil {
		if b, err := os.ReadFile(f); err == nil {
			cur = sanitize(b)
		}
	}
	s.cached = &cur
	return cur
}

// writeAtomic writes data to path through a temp file in the same folder and a
// rename, so a crash never leaves a half-written file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// hasKey asks the Keychain once per provider and remembers the answer for the
// session, also when it was an error: a denied prompt must not come back on
// every Get. SetKey updates the answer. s.mu must NOT be held.
func (s *Store) hasKey(p model.AiProvider) bool {
	s.mu.Lock()
	v, ok := s.present[p]
	s.mu.Unlock()
	if ok {
		return v
	}
	if s.Secrets == nil {
		return false
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	s.mu.Lock()
	v, ok = s.present[p]
	s.mu.Unlock()
	if ok {
		return v
	}
	b, err := s.Secrets.Get(keyName(p))
	has := err == nil && len(b) > 0
	s.remember(p, has)
	return has
}

// remember records whether a key is stored. It takes s.mu itself.
func (s *Store) remember(p model.AiProvider, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.present == nil {
		s.present = map[model.AiProvider]bool{}
	}
	s.present[p] = v
}

// view builds the page's settings. s.mu must NOT be held.
func (s *Store) view() model.Settings {
	s.mu.Lock()
	st := s.load()
	s.mu.Unlock()
	return model.Settings{
		AiProvider: st.AiProvider, HasClaudeKey: s.hasKey(model.ProviderClaude), HasOpenAiKey: s.hasKey(model.ProviderChatGPT),
		Background: st.Background, LargeFileMinMb: st.LargeFileMinMb, ConfirmBeforeClean: st.ConfirmBeforeClean,
	}
}

// Get returns the settings.
func (s *Store) Get() model.Settings {
	return s.view()
}

// Provider returns the assistant backend in use.
func (s *Store) Provider() model.AiProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load().AiProvider
}

// Patch validates and applies the fields that are set, and saves the file.
func (s *Store) Patch(p model.SettingsPatch) (model.Settings, error) {
	if err := s.patch(p); err != nil {
		return model.Settings{}, err
	}
	return s.view(), nil
}

func (s *Store) patch(p model.SettingsPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.load()
	if p.AiProvider != nil {
		if !validProvider(*p.AiProvider) {
			return errors.New("Unknown assistant provider.")
		}
		next.AiProvider = *p.AiProvider
	}
	if p.Background != nil {
		if !validBackground(*p.Background) {
			return errors.New("Unknown background style.")
		}
		next.Background = *p.Background
	}
	if p.LargeFileMinMb != nil {
		if *p.LargeFileMinMb < minLargeMb || *p.LargeFileMinMb > maxLargeMb {
			return fmt.Errorf("Large file size must be between %d MB and %d MB.", minLargeMb, maxLargeMb)
		}
		next.LargeFileMinMb = *p.LargeFileMinMb
	}
	if p.ConfirmBeforeClean != nil {
		next.ConfirmBeforeClean = *p.ConfirmBeforeClean
	}
	f, err := s.file()
	if err != nil {
		return errors.New("The settings folder is not available.")
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(f, data); err != nil {
		return errors.New("The settings could not be saved.")
	}
	s.cached = &next
	return nil
}

func providerName(p model.AiProvider) string {
	if p == model.ProviderChatGPT {
		return "OpenAI"
	}
	return "Claude"
}

// SetKey stores the API key in the Keychain, or removes it when key is nil or blank.
func (s *Store) SetKey(p model.AiProvider, key *string) (model.Settings, error) {
	if err := s.setKey(p, key); err != nil {
		return model.Settings{}, err
	}
	return s.view(), nil
}

func (s *Store) setKey(p model.AiProvider, key *string) error {
	if !validProvider(p) {
		return errors.New("Unknown assistant provider.")
	}
	if s.Secrets == nil {
		return errors.New("Secure storage is not available on this system, so the key was not saved.")
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if key == nil || strings.TrimSpace(*key) == "" {
		if err := s.Secrets.Delete(keyName(p)); err != nil && !errors.Is(err, ErrNotFound) {
			return errors.New("The key could not be removed from the Keychain.")
		}
		s.remember(p, false)
		return nil
	}
	clean := strings.TrimSpace(*key)
	if len(clean) < 20 || len(clean) > 512 || strings.IndexFunc(clean, isSpace) >= 0 {
		return fmt.Errorf("That does not look like a valid %s API key.", providerName(p))
	}
	if err := s.Secrets.Set(keyName(p), []byte(clean)); err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return errors.New("Secure storage is not available on this system, so the key was not saved.")
		}
		return errors.New("The key could not be saved to the Keychain.")
	}
	s.remember(p, true)
	return nil
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

// Key returns the stored API key, or "" when there is none. Only the assistant
// may call it; the result must never reach the page or a log.
func (s *Store) Key(p model.AiProvider) (string, error) {
	if s.Secrets == nil {
		return "", nil
	}
	s.keyMu.Lock() // one prompt at a time; s.mu is not held
	defer s.keyMu.Unlock()
	v, err := s.Secrets.Get(keyName(p))
	if errors.Is(err, ErrNotFound) {
		s.remember(p, false)
		return "", nil
	}
	if err != nil {
		return "", errors.New("The saved API key could not be read from the Keychain. Please add it again in Settings.")
	}
	key := strings.TrimSpace(string(v))
	s.remember(p, key != "")
	return key, nil
}
