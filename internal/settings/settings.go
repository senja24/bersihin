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
	"unicode"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/endpoint"
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

// storedEndpoint is a compatible provider's address (normalised by
// endpoint.Parse) and model. "" means not set.
type storedEndpoint struct {
	BaseUrl string `json:"baseUrl"`
	Model   string `json:"model"`
}

type stored struct {
	AiProvider         model.AiProvider `json:"aiProvider"`
	ClaudeCompat       storedEndpoint   `json:"claudeCompat"`
	OpenAiCompat       storedEndpoint   `json:"openAiCompat"`
	Background         model.Background `json:"background"`
	LargeFileMinMb     int              `json:"largeFileMinMb"`
	ConfirmBeforeClean bool             `json:"confirmBeforeClean"`
}

func defaults() stored {
	return stored{AiProvider: model.ProviderClaude, Background: model.BackgroundMist, LargeFileMinMb: 500, ConfirmBeforeClean: true}
}

func validProvider(p model.AiProvider) bool {
	return p == model.ProviderClaude || p == model.ProviderChatGPT || p.Compatible()
}

// compat returns the stored endpoint of a compatible provider.
func (st *stored) compat(p model.AiProvider) *storedEndpoint {
	if p == model.ProviderClaudeCompat {
		return &st.ClaudeCompat
	}
	return &st.OpenAiCompat
}

// sanitizeEndpoint takes a stored endpoint leniently: an address that no
// longer passes endpoint.Parse, or a bad model name, becomes "".
func sanitizeEndpoint(b json.RawMessage) storedEndpoint {
	var in storedEndpoint
	var out storedEndpoint
	if json.Unmarshal(b, &in) != nil {
		return out
	}
	if e, err := endpoint.Parse(in.BaseUrl); err == nil {
		out.BaseUrl = e.Base
	}
	if m := strings.TrimSpace(in.Model); endpoint.CheckModel(m) == nil {
		out.Model = m
	}
	return out
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

	// patchMu serialises Patch from start to end, Keychain call included, so a
	// host change and the deletion of its key are one step.
	patchMu sync.Mutex
	// keyMu serialises Keychain calls. mu guards the fields below and is never
	// held across a Keychain call (a prompt can stay up for a long time).
	keyMu   sync.Mutex
	mu      sync.Mutex
	cached  *stored
	present map[model.AiProvider]bool // what the Keychain said once; kept for the session
}

func keyName(p model.AiProvider) string {
	switch p {
	case model.ProviderChatGPT:
		return config.OpenAIKeyName
	case model.ProviderClaudeCompat:
		return config.ClaudeCompatKeyName
	case model.ProviderOpenAICompat:
		return config.OpenAICompatKeyName
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
	out.ClaudeCompat = sanitizeEndpoint(raw["claudeCompat"])
	out.OpenAiCompat = sanitizeEndpoint(raw["openAiCompat"])
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
		ClaudeCompat: model.AiEndpoint{BaseUrl: st.ClaudeCompat.BaseUrl, Model: st.ClaudeCompat.Model, HasKey: s.hasKey(model.ProviderClaudeCompat)},
		OpenAiCompat: model.AiEndpoint{BaseUrl: st.OpenAiCompat.BaseUrl, Model: st.OpenAiCompat.Model, HasKey: s.hasKey(model.ProviderOpenAICompat)},
		Background:   st.Background, LargeFileMinMb: st.LargeFileMinMb, ConfirmBeforeClean: st.ConfirmBeforeClean,
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

// Endpoint returns the saved address and model of a compatible provider
// (HasKey is not filled). Both are "" when not set or not valid.
func (s *Store) Endpoint(p model.AiProvider) model.AiEndpoint {
	if !p.Compatible() {
		return model.AiEndpoint{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.load()
	e := st.compat(p)
	return model.AiEndpoint{BaseUrl: e.BaseUrl, Model: e.Model}
}

// Patch validates and applies the fields that are set, and saves the file.
// When a compatible provider's address moves to another host (or is cleared)
// its stored key is deleted first, so a key never goes to a host it was not
// saved for; if the deletion fails nothing is changed.
func (s *Store) Patch(p model.SettingsPatch) (model.Settings, error) {
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	next, wipe, bind, err := s.plan(p)
	if err != nil {
		return model.Settings{}, err
	}
	for _, k := range wipe {
		if err := s.dropKey(k); err != nil {
			return model.Settings{}, errors.New("The saved key could not be removed from the Keychain, so the address was not changed.")
		}
	}
	for k, authority := range bind {
		if err := s.bindKey(k, authority); err != nil {
			return model.Settings{}, errors.New("The saved key could not be updated in the Keychain, so the address was not changed.")
		}
	}
	if err := s.commit(next); err != nil {
		return model.Settings{}, err
	}
	return s.view(), nil
}

// boundKey is what the Keychain holds for a compatible provider: the key and
// the authority (host:port) it was saved for. A key is only ever sent to that
// authority, whatever the settings file says. Authority "" is a key saved
// before any address: it is bound when the first address is saved.
type boundKey struct {
	V         int    `json:"v"`
	Authority string `json:"authority"`
	Key       string `json:"key"`
}

func encodeBound(authority, key string) []byte {
	b, _ := json.Marshal(boundKey{V: 1, Authority: authority, Key: key})
	return b
}

func decodeBound(b []byte) (boundKey, bool) {
	var k boundKey
	if json.Unmarshal(b, &k) != nil || k.V != 1 || k.Key == "" {
		return boundKey{}, false
	}
	return k, true
}

// errNotBound is returned when the stored key was not saved for the address
// it would be sent to.
var errNotBound = errors.New("The saved API key was not saved for this address. Save the API key again in Settings.")

// savedAuthority is the authority of the saved address ("" when none).
func (s *Store) savedAuthority(p model.AiProvider) string {
	s.mu.Lock()
	st := s.load()
	base := st.compat(p).BaseUrl
	s.mu.Unlock()
	if e, err := endpoint.Parse(base); err == nil {
		return e.Authority()
	}
	return ""
}

// bindKey is called when the first address is saved. A key saved before any
// address becomes bound to it; a key bound to another authority (for example
// from an address that was dropped as invalid) is deleted.
func (s *Store) bindKey(p model.AiProvider, authority string) error {
	if s.Secrets == nil {
		return nil
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	v, err := s.Secrets.Get(keyName(p))
	if errors.Is(err, ErrNotFound) {
		s.remember(p, false)
		return nil
	}
	if err != nil {
		return err
	}
	bk, ok := decodeBound(v)
	switch {
	case ok && bk.Authority == authority:
		return nil
	case ok && bk.Authority == "":
		return s.Secrets.Set(keyName(p), encodeBound(authority, bk.Key))
	}
	if err := s.Secrets.Delete(keyName(p)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.remember(p, false)
	return nil
}

// dropKey removes a provider's key from the Keychain and from the presence cache.
func (s *Store) dropKey(p model.AiProvider) error {
	if s.Secrets == nil {
		s.remember(p, false)
		return nil
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if err := s.Secrets.Delete(keyName(p)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.remember(p, false)
	return nil
}

// applyEndpoint applies a patch to a stored endpoint and says whether the
// host (and port) changed from a previously set one.
func applyEndpoint(cur storedEndpoint, patch *model.AiEndpointPatch) (next storedEndpoint, hostChanged, firstSet bool, err error) {
	next = cur
	if patch.BaseUrl != nil {
		raw := strings.TrimSpace(*patch.BaseUrl)
		if raw == "" {
			next.BaseUrl = ""
		} else {
			e, err := endpoint.Parse(raw)
			if err != nil {
				return cur, false, false, err
			}
			next.BaseUrl = e.Base
		}
	}
	if patch.Model != nil {
		m := strings.TrimSpace(*patch.Model)
		if m != "" {
			if err := endpoint.CheckModel(m); err != nil {
				return cur, false, false, err
			}
		}
		next.Model = m
	}
	if cur.BaseUrl != "" && next.BaseUrl != cur.BaseUrl {
		old, err1 := endpoint.Parse(cur.BaseUrl)
		now, err2 := endpoint.Parse(next.BaseUrl)
		hostChanged = err1 != nil || err2 != nil || !old.SameHost(now)
	}
	firstSet = cur.BaseUrl == "" && next.BaseUrl != ""
	return next, hostChanged, firstSet, nil
}

// plan validates the patch and returns the next settings, the providers whose
// key must be deleted (host changed) and those whose key must be bound to a
// first address. It takes s.mu.
func (s *Store) plan(p model.SettingsPatch) (next stored, wipe []model.AiProvider, bind map[model.AiProvider]string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next = s.load()
	if p.AiProvider != nil {
		if !validProvider(*p.AiProvider) {
			return next, nil, nil, errors.New("Unknown assistant provider.")
		}
		next.AiProvider = *p.AiProvider
	}
	for _, c := range []struct {
		p     model.AiProvider
		patch *model.AiEndpointPatch
	}{{model.ProviderClaudeCompat, p.ClaudeCompat}, {model.ProviderOpenAICompat, p.OpenAiCompat}} {
		if c.patch == nil {
			continue
		}
		e := next.compat(c.p)
		ne, changed, first, err := applyEndpoint(*e, c.patch)
		if err != nil {
			return next, nil, nil, err
		}
		*e = ne
		if changed {
			wipe = append(wipe, c.p)
		}
		if first {
			parsed, perr := endpoint.Parse(ne.BaseUrl)
			if perr != nil {
				return next, nil, nil, perr
			}
			if bind == nil {
				bind = map[model.AiProvider]string{}
			}
			bind[c.p] = parsed.Authority()
		}
	}
	err = s.checkPatch(&next, p)
	return next, wipe, bind, err
}

// checkPatch applies the plain preferences of a patch.
func (s *Store) checkPatch(next *stored, p model.SettingsPatch) error {
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
	return nil
}

// commit writes the settings file and makes them current. It takes s.mu.
func (s *Store) commit(next stored) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	switch p {
	case model.ProviderChatGPT:
		return "OpenAI"
	case model.ProviderClaudeCompat:
		return "Claude-compatible"
	case model.ProviderOpenAICompat:
		return "OpenAI-compatible"
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
	s.patchMu.Lock() // the address cannot change while the key is bound to it
	defer s.patchMu.Unlock()
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
	if p.Compatible() {
		// Any length from 1: local servers use short keys. Printable ASCII only,
		// since the key goes into an HTTP header.
		if len(clean) < 1 || len(clean) > 512 || strings.IndexFunc(clean, func(r rune) bool { return r <= ' ' || r > '~' || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("That does not look like a valid %s API key.", providerName(p))
		}
	} else if len(clean) < 20 || len(clean) > 512 || strings.IndexFunc(clean, isSpace) >= 0 {
		return fmt.Errorf("That does not look like a valid %s API key.", providerName(p))
	}
	value := []byte(clean)
	if p.Compatible() {
		value = encodeBound(s.savedAuthority(p), clean)
	}
	if err := s.Secrets.Set(keyName(p), value); err != nil {
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
// may call it; the result must never reach the page or a log. For a
// compatible provider the key is returned only if it was saved for the
// address that is saved now.
func (s *Store) Key(p model.AiProvider) (string, error) {
	if p.Compatible() {
		return s.KeyFor(p, s.savedAuthority(p))
	}
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

// KeyFor returns a compatible provider's key for sending to authority
// (host:port). It refuses unless the key was saved for exactly that authority
// and the saved address still has it, so a key never goes to a host it was not
// saved for, however the settings file or the settings changed meanwhile.
func (s *Store) KeyFor(p model.AiProvider, authority string) (string, error) {
	if !p.Compatible() {
		return s.Key(p)
	}
	if s.Secrets == nil {
		return "", nil
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	v, err := s.Secrets.Get(keyName(p))
	if errors.Is(err, ErrNotFound) {
		s.remember(p, false)
		return "", nil
	}
	if err != nil {
		return "", errors.New("The saved API key could not be read from the Keychain. Please add it again in Settings.")
	}
	s.remember(p, true)
	bk, ok := decodeBound(v)
	if !ok || authority == "" || bk.Authority != authority || s.savedAuthority(p) != authority {
		return "", errNotBound
	}
	return bk.Key, nil
}
