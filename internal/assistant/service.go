package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/endpoint"
	"dev.agus.bersihin/internal/model"
)

// Service answers questions. The provider comes from the settings, the key
// from the Keychain.
type Service struct {
	Home string
	// Provider returns the chosen backend; Key its API key ("" when none).
	Provider func() model.AiProvider
	Key      func(model.AiProvider) (string, error)
	// KeyFor returns a compatible provider's key for sending to one address
	// (endpoint.Binding: scheme, host, port, path) and refuses a key saved for
	// another. When nil, Key is used.
	KeyFor func(p model.AiProvider, binding string) (string, error)
	// OfficialModel returns the model id chosen for Claude or ChatGPT, "" for
	// the default of internal/config. May be nil.
	OfficialModel func(model.AiProvider) string
	// Endpoint returns the saved address and model of a compatible provider.
	Endpoint func(model.AiProvider) model.AiEndpoint
	// Version is the app version, for the User-Agent.
	Version func() string

	HTTP *http.Client
	// AnthropicURL and OpenAIURL override the endpoints of internal/config (tests).
	AnthropicURL string
	OpenAIURL    string
	// Lookup resolves a host name for a plain-http (loopback) target; default:
	// the system resolver (tests).
	Lookup lookupFunc

	modeMu sync.Mutex
	modes  map[string]mode // what worked per provider, address and model
}

func (s *Service) endpoint(p model.AiProvider) model.AiEndpoint {
	if s.Endpoint == nil || !p.Compatible() {
		return model.AiEndpoint{}
	}
	return s.Endpoint(p)
}

// keyFor reads the key of provider p. For a compatible provider the key must
// have been saved for the very address that is about to be called.
func (s *Service) keyFor(p model.AiProvider, ep model.AiEndpoint) (string, *callError) {
	if p.Compatible() {
		e, err := endpoint.Parse(ep.BaseUrl)
		if err != nil {
			return "", &callError{code: codeBadURL, msg: err.Error()}
		}
		if s.KeyFor != nil {
			key, err := s.KeyFor(p, e.Binding())
			if err != nil {
				return "", &callError{code: codeNoKey, msg: err.Error()}
			}
			return key, nil
		}
	}
	key, err := s.Key(p)
	if err != nil {
		return "", &callError{code: codeOther, msg: err.Error()}
	}
	return key, nil
}

// sameSavedAddress reports whether two saved endpoints have the same full
// address (scheme, host, port and path): the address a stored key is bound to.
func sameSavedAddress(a, b model.AiEndpoint) bool {
	ea, err1 := endpoint.Parse(a.BaseUrl)
	eb, err2 := endpoint.Parse(b.BaseUrl)
	return err1 == nil && err2 == nil && ea.Binding() == eb.Binding()
}

// Ask sends the question and the (redacted) context to the chosen provider
// and returns its checked answer.
func (s *Service) Ask(ctx context.Context, question string, c model.AiContext) (model.AiAdvice, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return model.AiAdvice{}, errors.New("Type a question for the assistant first.")
	}
	if len([]rune(q)) > maxQuestion {
		return model.AiAdvice{}, fmt.Errorf("Please keep your question under %d characters.", maxQuestion)
	}
	provider := s.Provider()
	ep := s.endpoint(provider)
	if provider.Compatible() && (ep.BaseUrl == "" || ep.Model == "") {
		return model.AiAdvice{}, fmt.Errorf("Set up your %s provider in Settings first: add the address, the model name and the API key.", providerLabel(provider))
	}
	key, kce := s.keyFor(provider, ep)
	if kce != nil {
		if kce.code == codeBadURL {
			return model.AiAdvice{}, askError(provider, kce)
		}
		return model.AiAdvice{}, errors.New(kce.msg)
	}
	if key == "" {
		switch provider {
		case model.ProviderClaude:
			return model.AiAdvice{}, errors.New("Add your Claude API key in Settings to use the assistant.")
		case model.ProviderChatGPT:
			return model.AiAdvice{}, errors.New("Add your ChatGPT (OpenAI) API key in Settings to use the assistant.")
		}
		return model.AiAdvice{}, fmt.Errorf("Add the API key for your %s provider in Settings to use the assistant.", providerLabel(provider))
	}

	data, known := BuildContext(s.Home, c)
	user, err := UserMessage(s.Home, q, data)
	if err != nil {
		return model.AiAdvice{}, err
	}
	t, ce := s.target(provider, ep, key)
	if ce != nil {
		return model.AiAdvice{}, askError(provider, ce)
	}
	// The key was read after the address: if the address changed in between,
	// the key may belong to the new one.
	if provider.Compatible() && !sameSavedAddress(ep, s.endpoint(provider)) {
		return model.AiAdvice{}, errors.New("The settings changed while the question was sent. Please try again.")
	}
	r, _, ce := s.exchange(ctx, t, SystemPrompt, user, config.AssistantMaxTokens, config.AssistantTimeout)
	if ce != nil {
		return model.AiAdvice{}, askError(provider, ce)
	}
	switch r.kind {
	case replyUnreadable:
		return model.AiAdvice{}, errUnreadable(provider)
	case replyRefused:
		return model.AiAdvice{}, errRefused(provider)
	case replyCutOff:
		return model.AiAdvice{}, errCutOff
	}
	raw := []byte(r.text)
	if provider.Compatible() {
		// Such servers wrap the JSON in text, fences or reasoning.
		if raw, err = extractAdvice(r.text); err != nil {
			return model.AiAdvice{}, errUnreadable(provider)
		}
	}
	return ParseAdvice(s.Home, raw, known, provider)
}

func providerLabel(p model.AiProvider) string {
	if p == model.ProviderClaudeCompat {
		return "Claude-compatible"
	}
	return "OpenAI-compatible"
}

// TestConnection sends one tiny request to the provider with the SAVED key and
// reports how it went. A draft address must be the saved one: a stored key is
// never sent to an address that has not been saved.
// Failures are results (OK false), not errors; only a cancelled context is an
// error.
func (s *Service) TestConnection(ctx context.Context, p model.AiProvider, draft *model.AiEndpointPatch) (model.AiTestResult, error) {
	start := time.Now()
	fail := func(code, msg, detail string) (model.AiTestResult, error) {
		if msg == "" {
			msg = codeMessages[code]
		}
		return model.AiTestResult{Code: code, Message: msg, Detail: detail, Ms: time.Since(start).Milliseconds()}, nil
	}

	var ep model.AiEndpoint
	if p.Compatible() {
		saved := s.endpoint(p)
		ep = saved
		if draft != nil && draft.BaseUrl != nil {
			raw := strings.TrimSpace(*draft.BaseUrl)
			de, err := endpoint.Parse(raw)
			if err != nil {
				return fail(codeBadURL, err.Error(), "")
			}
			if !sameSavedAddress(saved, model.AiEndpoint{BaseUrl: de.Base}) {
				return fail(codeBadURL, "The server changed. Save the new address first, then test the connection.", "")
			}
			ep.BaseUrl = de.Base
		}
		if draft != nil && draft.Model != nil {
			ep.Model = strings.TrimSpace(*draft.Model)
		}
		if ep.BaseUrl == "" {
			return fail(codeBadURL, "Enter the address of the server.", "")
		}
		if endpoint.CheckModel(ep.Model) != nil {
			return fail(codeNoModel, "", "")
		}
	} else if p != model.ProviderClaude && p != model.ProviderChatGPT {
		return fail(codeOther, "Unknown assistant provider.", "")
	}

	key, kce := s.keyFor(p, ep)
	if kce != nil {
		return fail(kce.code, kce.msg, "")
	}
	if key == "" {
		return fail(codeNoKey, "", "")
	}
	t, ce := s.target(p, ep, key)
	if ce != nil {
		return fail(ce.code, ce.msg, "")
	}
	if p.Compatible() && !sameSavedAddress(ep, s.endpoint(p)) {
		return fail(codeBadURL, "The settings changed during the test. Try again.", "")
	}

	user, err := UserMessage(s.Home, "ping", contextData{})
	if err != nil {
		return fail(codeOther, "", "")
	}
	r, m, ce := s.exchange(ctx, t, SystemPrompt, user, config.AssistantTestMaxTokens, config.AssistantTestTimeout)
	if ce != nil {
		if ce.err != nil {
			return model.AiTestResult{}, ce.err
		}
		return fail(ce.code, ce.msg, ce.detail)
	}
	if strings.TrimSpace(r.text) == "" {
		return fail(codeNotJSON, "", t.hostPath)
	}
	res := model.AiTestResult{OK: true, Code: codeOK, Message: codeMessages[codeOK], Ms: time.Since(start).Milliseconds()}
	if p.Compatible() {
		res.Mode = string(m)
	}
	return res, nil
}
