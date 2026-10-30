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
	ClaudeModel        string           `json:"claudeModel"`
	OpenAiModel        string           `json:"openAiModel"`
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

// sanitizeModel takes a stored model id of an official provider leniently: a
// bad one becomes "" (the default).
func sanitizeModel(b json.RawMessage) string {
	var m string
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	if m = strings.TrimSpace(m); endpoint.CheckModel(m) == nil {
		return m
	}
	return ""
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
	present map[model.AiProvider]keyInfo // what the Keychain said once; kept for the session
}

// keyInfo is what is known about a stored key without keeping the key: that
// there is one and, for a compatible provider, the address it is bound to.
type keyInfo struct {
	has     bool
	decoded bool   // a compatible entry in the current format
	bound   string // its address binding ("" until the first address is saved)
}

// infoOf describes a Keychain value.
func infoOf(p model.AiProvider, v []byte) keyInfo {
	if p.Compatible() {
		bk, ok := decodeBound(v)
		return keyInfo{has: len(v) > 0, decoded: ok, bound: bk.Address}
	}
	return keyInfo{has: len(v) > 0}
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
	out.ClaudeModel = sanitizeModel(raw["claudeModel"])
	out.OpenAiModel = sanitizeModel(raw["openAiModel"])
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

// info asks the Keychain once per provider and remembers the answer for the
// session, also when it was an error: a denied prompt must not come back on
// every Get. The setters update the answer. s.mu must NOT be held.
func (s *Store) info(p model.AiProvider) keyInfo {
	s.mu.Lock()
	v, ok := s.present[p]
	s.mu.Unlock()
	if ok {
		return v
	}
	if s.Secrets == nil {
		return keyInfo{}
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
	in := keyInfo{}
	if err == nil {
		in = infoOf(p, b)
	}
	s.remember(p, in)
	return in
}

func (s *Store) hasKey(p model.AiProvider) bool { return s.info(p).has }

// remember records what is stored. It takes s.mu itself.
func (s *Store) remember(p model.AiProvider, in keyInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.present == nil {
		s.present = map[model.AiProvider]keyInfo{}
	}
	s.present[p] = in
}

// bindingOf is the key binding of a saved address ("" when none or invalid).
func bindingOf(base string) string {
	if e, err := endpoint.Parse(base); err == nil {
		return e.Binding()
	}
	return ""
}

// hostOfBinding is the host (and non-default port) of a binding, for
// messages; "" when it is empty or not an address. The binding may come from
// the Keychain, so it is parsed again and never shown as it is.
func hostOfBinding(binding string) string {
	if e, err := endpoint.Parse(binding); err == nil {
		return e.Display()
	}
	return ""
}

// keyStateOf says whether the stored key can be used for the saved binding.
func keyStateOf(in keyInfo, saved string) (model.KeyState, string) {
	switch {
	case !in.has:
		return model.KeyNone, ""
	case in.decoded && in.bound == saved:
		return model.KeyOK, ""
	}
	return model.KeyOtherAddress, hostOfBinding(in.bound)
}

func (s *Store) endpointView(p model.AiProvider, e storedEndpoint) model.AiEndpoint {
	state, host := keyStateOf(s.info(p), bindingOf(e.BaseUrl))
	return model.AiEndpoint{BaseUrl: e.BaseUrl, Model: e.Model, HasKey: state == model.KeyOK, KeyState: state, KeyHost: host}
}

// view builds the page's settings. s.mu must NOT be held.
func (s *Store) view() model.Settings {
	s.mu.Lock()
	st := s.load()
	s.mu.Unlock()
	return model.Settings{
		AiProvider: st.AiProvider, HasClaudeKey: s.hasKey(model.ProviderClaude), HasOpenAiKey: s.hasKey(model.ProviderChatGPT),
		ClaudeCompat: s.endpointView(model.ProviderClaudeCompat, st.ClaudeCompat),
		OpenAiCompat: s.endpointView(model.ProviderOpenAICompat, st.OpenAiCompat),
		ClaudeModel:  st.ClaudeModel, OpenAiModel: st.OpenAiModel,
		DefaultClaudeModel: config.ClaudeModel, DefaultOpenAiModel: config.OpenAIModel,
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

// OfficialModel returns the model id chosen for an official provider, or ""
// for the default (and for any other provider).
func (s *Store) OfficialModel(p model.AiProvider) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch p {
	case model.ProviderClaude:
		return s.load().ClaudeModel
	case model.ProviderChatGPT:
		return s.load().OpenAiModel
	}
	return ""
}

// Endpoint returns the saved address and model of a compatible provider
// (the key fields are not filled). Both are "" when not set or not valid.
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
// When a compatible provider's address moves to another origin (scheme, host
// or port) or is cleared, its stored key is deleted first, so a key never goes
// to an address it was not saved for; if the deletion fails nothing is
// changed. A change of the path only (same origin) moves the key's binding to
// the new address, since the page shows that as a safe edit.
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
	for k, ch := range bind {
		if err := s.bindKey(k, ch.from, ch.to); err != nil {
			return model.Settings{}, errors.New("The saved key could not be updated in the Keychain, so the address was not changed.")
		}
	}
	if err := s.commit(next); err != nil {
		return model.Settings{}, err
	}
	return s.view(), nil
}

// boundKey is what the Keychain holds for a compatible provider: the key and
// the address (endpoint.Binding: scheme, host, port, path) it was saved for. A
// key is only ever sent to that address, whatever the settings file says.
// Address "" is a key saved before any address: it is bound when the first
// address is saved. Entries of format 1 (host and port only, no scheme or
// path) are not trusted: decodeBound refuses them and the key must be entered
// again once.
type boundKey struct {
	V       int    `json:"v"`
	Address string `json:"address"`
	Key     string `json:"key"`
}

const boundVersion = 2

func encodeBound(address, key string) []byte {
	b, _ := json.Marshal(boundKey{V: boundVersion, Address: address, Key: key})
	return b
}

func decodeBound(b []byte) (boundKey, bool) {
	var k boundKey
	if json.Unmarshal(b, &k) != nil || k.V != boundVersion || k.Key == "" {
		return boundKey{}, false
	}
	return k, true
}

// notBoundError explains that the stored key cannot be used for the saved
// address. It names hosts only (never paths or keys).
func notBoundError(in keyInfo, saved string) error {
	const again = " Save the API key again in Settings."
	bound, now := hostOfBinding(in.bound), hostOfBinding(saved)
	switch {
	case !in.decoded:
		return errors.New("The saved API key is from an older version and is not tied to an address." + again)
	case saved == "":
		return errors.New("Enter the address of the server in Settings first.")
	case in.bound == "":
		return errors.New("The saved API key was entered before an address was saved." + again)
	case bound == "":
		return errors.New("The saved API key was saved for an unknown address." + again)
	case bound == now:
		return fmt.Errorf("The saved API key belongs to a different address on %s (another path or http/https than the saved one). If you did not change the address, it was changed outside the app.%s", now, again)
	}
	return fmt.Errorf("The saved API key belongs to %s, but the saved address is on %s. If you did not change the address, it was changed outside the app.%s", bound, now, again)
}

// savedBinding is the key binding of the saved address ("" when none).
func (s *Store) savedBinding(p model.AiProvider) string {
	s.mu.Lock()
	st := s.load()
	base := st.compat(p).BaseUrl
	s.mu.Unlock()
	return bindingOf(base)
}

// putSecret replaces a provider's Keychain item: it deletes the item first and
// adds a new one, since a plain update would keep an item that another app
// planted with an open access list. keyMu must be held.
func (s *Store) putSecret(p model.AiProvider, value []byte) error {
	name := keyName(p)
	if err := s.Secrets.Delete(name); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := s.Secrets.Set(name, value); err != nil {
		s.remember(p, keyInfo{}) // the old item is gone
		return err
	}
	return nil
}

// bindMove is a key's binding going from one address to another.
type bindMove struct{ from, to string }

// bindKey is called when the saved address gets a new binding without a new
// origin: the first address, or a changed path. A key bound to from moves to
// to; one already bound to to stays; any other key (for example from an
// address that was dropped as invalid) is deleted.
func (s *Store) bindKey(p model.AiProvider, from, to string) error {
	if s.Secrets == nil {
		return nil
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	v, err := s.Secrets.Get(keyName(p))
	if errors.Is(err, ErrNotFound) {
		s.remember(p, keyInfo{})
		return nil
	}
	if err != nil {
		return err
	}
	bk, ok := decodeBound(v)
	switch {
	case ok && bk.Address == to:
		return nil
	case ok && bk.Address == from:
		if err := s.putSecret(p, encodeBound(to, bk.Key)); err != nil {
			return err
		}
		s.remember(p, keyInfo{has: true, decoded: true, bound: to})
		return nil
	}
	if err := s.Secrets.Delete(keyName(p)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.remember(p, keyInfo{})
	return nil
}

// dropKey removes a provider's key from the Keychain and from the presence cache.
func (s *Store) dropKey(p model.AiProvider) error {
	if s.Secrets == nil {
		s.remember(p, keyInfo{})
		return nil
	}
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if err := s.Secrets.Delete(keyName(p)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.remember(p, keyInfo{})
	return nil
}

// addressChange is what a patch does to the binding of a stored key.
type addressChange int

const (
	addressSame   addressChange = iota // nothing to do
	addressFirst                       // the first address: an unbound key is bound to it
	addressMoved                       // same scheme, host and port, other path: the key follows
	addressOrigin                      // other scheme, host or port, or cleared: the key is deleted
)

// applyEndpoint applies a patch to a stored endpoint and says what it does to
// the key.
func applyEndpoint(cur storedEndpoint, patch *model.AiEndpointPatch) (next storedEndpoint, ch addressChange, err error) {
	next = cur
	if patch.BaseUrl != nil {
		raw := strings.TrimSpace(*patch.BaseUrl)
		if raw == "" {
			next.BaseUrl = ""
		} else {
			e, err := endpoint.Parse(raw)
			if err != nil {
				return cur, addressSame, err
			}
			next.BaseUrl = e.Base
		}
	}
	if patch.Model != nil {
		m := strings.TrimSpace(*patch.Model)
		if m != "" {
			if err := endpoint.CheckModel(m); err != nil {
				return cur, addressSame, err
			}
		}
		next.Model = m
	}
	switch {
	case cur.BaseUrl == next.BaseUrl:
	case cur.BaseUrl == "":
		ch = addressFirst
	case next.BaseUrl == "":
		ch = addressOrigin
	default:
		old, err1 := endpoint.Parse(cur.BaseUrl)
		now, err2 := endpoint.Parse(next.BaseUrl)
		switch {
		case err1 != nil || err2 != nil || !old.SameOrigin(now):
			ch = addressOrigin
		case old.Binding() != now.Binding():
			ch = addressMoved
		}
	}
	return next, ch, nil
}

// plan validates the patch and returns the next settings, the providers whose
// key must be deleted (origin changed) and the keys whose binding must move
// (first address, changed path). It takes s.mu.
func (s *Store) plan(p model.SettingsPatch) (next stored, wipe []model.AiProvider, bind map[model.AiProvider]bindMove, err error) {
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
		old := bindingOf(e.BaseUrl)
		ne, ch, err := applyEndpoint(*e, c.patch)
		if err != nil {
			return next, nil, nil, err
		}
		*e = ne
		switch ch {
		case addressOrigin:
			wipe = append(wipe, c.p)
		case addressFirst, addressMoved:
			if bind == nil {
				bind = map[model.AiProvider]bindMove{}
			}
			bind[c.p] = bindMove{from: old, to: bindingOf(ne.BaseUrl)}
		}
	}
	err = s.checkPatch(&next, p)
	return next, wipe, bind, err
}

// checkPatch applies the plain preferences of a patch.
func (s *Store) checkPatch(next *stored, p model.SettingsPatch) error {
	for _, m := range []struct {
		patch *string
		to    *string
	}{{p.ClaudeModel, &next.ClaudeModel}, {p.OpenAiModel, &next.OpenAiModel}} {
		if m.patch == nil {
			continue
		}
		v := strings.TrimSpace(*m.patch)
		if v != "" {
			if err := endpoint.CheckModel(v); err != nil {
				return err
			}
		}
		*m.to = v
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

// SetKey stores the API key in the Keychain, or removes it when key is nil or
// blank. For a compatible provider forBaseUrl is the address the page shows:
// the key is bound to the saved address, so a save is refused unless that is
// the very address on screen (save the address first, then the key). Removing
// a key needs no address.
func (s *Store) SetKey(p model.AiProvider, key *string, forBaseUrl string) (model.Settings, error) {
	if err := s.setKey(p, key, forBaseUrl); err != nil {
		return model.Settings{}, err
	}
	return s.view(), nil
}

func (s *Store) setKey(p model.AiProvider, key *string, forBaseUrl string) error {
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
		s.remember(p, keyInfo{})
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
	info := keyInfo{has: true}
	if p.Compatible() {
		saved := s.savedBinding(p)
		shown := ""
		if raw := strings.TrimSpace(forBaseUrl); raw != "" {
			e, err := endpoint.Parse(raw)
			if err != nil {
				return err
			}
			shown = e.Binding()
		}
		if shown != saved {
			if saved == "" {
				return errors.New("Save the address first, then the API key.")
			}
			return fmt.Errorf("The address on screen is not the saved address (%s). Save the address first, then the API key.", hostOfBinding(saved))
		}
		value = encodeBound(saved, clean)
		info = keyInfo{has: true, decoded: true, bound: saved}
	}
	if err := s.putSecret(p, value); err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return errors.New("Secure storage is not available on this system, so the key was not saved.")
		}
		return errors.New("The key could not be saved to the Keychain.")
	}
	s.remember(p, info)
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
		return s.KeyFor(p, s.savedBinding(p))
	}
	if s.Secrets == nil {
		return "", nil
	}
	s.keyMu.Lock() // one prompt at a time; s.mu is not held
	defer s.keyMu.Unlock()
	v, err := s.Secrets.Get(keyName(p))
	if errors.Is(err, ErrNotFound) {
		s.remember(p, keyInfo{})
		return "", nil
	}
	if err != nil {
		return "", errors.New("The saved API key could not be read from the Keychain. Please add it again in Settings.")
	}
	key := strings.TrimSpace(string(v))
	s.remember(p, keyInfo{has: key != ""})
	return key, nil
}

// KeyFor returns a compatible provider's key for sending to the address with
// this binding (endpoint.Binding: scheme, host, port, path). It refuses unless
// the key was saved for exactly that address and the saved address still is
// it, so a key never goes to an address it was not saved for, however the
// settings file or the settings changed meanwhile. The refusal names the
// hosts involved.
func (s *Store) KeyFor(p model.AiProvider, binding string) (string, error) {
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
		s.remember(p, keyInfo{})
		return "", nil
	}
	if err != nil {
		return "", errors.New("The saved API key could not be read from the Keychain. Please add it again in Settings.")
	}
	in := infoOf(p, v)
	s.remember(p, in)
	saved := s.savedBinding(p)
	if in.decoded && binding != "" && in.bound == binding && saved == binding {
		bk, _ := decodeBound(v)
		return bk.Key, nil
	}
	if in.decoded && binding != "" && in.bound == saved {
		return "", errors.New("The settings changed while the request was prepared. Please try again.")
	}
	return "", notBoundError(in, saved)
}
