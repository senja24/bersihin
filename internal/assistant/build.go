package assistant

import (
	"net/url"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/endpoint"
	"dev.agus.bersihin/internal/model"
)

// mode is how the answer format is asked for. "structured" sends the
// provider's JSON output option; "plain" leaves it out and asks in the system
// prompt instead, for servers that reject the option.
type mode string

const (
	modeStructured mode = "structured"
	modePlain      mode = "plain"
)

// plainSuffix is added to the system prompt in plain mode.
const plainSuffix = "\n\nOutput only the JSON object. No code fences, no text before or after."

// adviceSchema is the JSON schema of the answer, for Claude's structured output.
var adviceSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"answer": map[string]any{"type": "string"},
		"suggestions": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"verdict": map[string]any{"type": "string", "enum": []string{"safe", "review", "keep"}},
					"reason":  map[string]any{"type": "string"},
				},
				"required":             []string{"path", "verdict", "reason"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"answer", "suggestions"},
	"additionalProperties": false,
}

// target is one resolved request destination: where to POST, with which
// headers and model, and what the transport must enforce.
type target struct {
	provider model.AiProvider
	url      string
	headers  map[string]string
	model    string
	// hostPath is host and path of url, the only part of it shown in a detail.
	hostPath string
	// httpLoopback: the URL is plain http, so the connection must really go to
	// this machine. loopback: the host is this machine (no proxy).
	httpLoopback bool
	loopback     bool
	authority    string
}

func (t target) compat() bool { return t.provider.Compatible() }

func (t target) claude() bool {
	return t.provider == model.ProviderClaude || t.provider == model.ProviderClaudeCompat
}

// target resolves the destination of provider p. The official providers use
// the endpoints and models of internal/config. A compatible provider uses ep,
// which is checked again here: a stored address is never trusted.
func (s *Service) target(p model.AiProvider, ep model.AiEndpoint, key string) (target, *callError) {
	t := target{provider: p, headers: map[string]string{}}
	switch p {
	case model.ProviderClaude:
		t.url, t.model = firstNonEmpty(s.AnthropicURL, config.AnthropicURL), config.ClaudeModel
	case model.ProviderChatGPT:
		t.url, t.model = firstNonEmpty(s.OpenAIURL, config.OpenAIURL), config.OpenAIModel
	case model.ProviderClaudeCompat, model.ProviderOpenAICompat:
		e, err := endpoint.Parse(ep.BaseUrl)
		if err != nil {
			return t, &callError{code: codeBadURL, msg: err.Error()}
		}
		if endpoint.CheckModel(ep.Model) != nil {
			return t, &callError{code: codeNoModel}
		}
		kind := endpoint.OpenAI
		if p == model.ProviderClaudeCompat {
			kind = endpoint.Claude
		}
		t.url, t.model = endpoint.Join(e.Base, kind), ep.Model
		t.authority, t.loopback = e.Authority(), e.Loopback
	default:
		return t, &callError{code: codeOther}
	}
	if u, err := url.Parse(t.url); err == nil {
		t.hostPath = u.Host + u.EscapedPath()
		t.httpLoopback = u.Scheme == "http"
	}

	if t.claude() {
		t.headers["x-api-key"] = key
		t.headers["anthropic-version"] = config.AnthropicVersion
	}
	if !t.claude() || p == model.ProviderClaudeCompat {
		// Claude-compatible gateways differ on which header they read; both
		// go to the same host.
		t.headers["authorization"] = "Bearer " + key
	}
	return t, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// body builds the request body. The official providers always ask for
// structured output and their bodies are exactly what they were before
// compatible providers existed.
func (t target) body(system, user string, maxTokens int, m mode) map[string]any {
	if m == modePlain {
		system += plainSuffix
	}
	if t.claude() {
		b := map[string]any{
			"model":      t.model,
			"max_tokens": maxTokens,
			"system":     system,
			"messages":   []map[string]any{{"role": "user", "content": user}},
		}
		if m == modeStructured {
			b["output_config"] = map[string]any{
				"format": map[string]any{"type": "json_schema", "schema": adviceSchema},
			}
		}
		return b
	}
	b := map[string]any{
		"model": t.model,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if t.compat() {
		b["max_tokens"] = maxTokens
		b["stream"] = false
	} else {
		b["max_completion_tokens"] = maxTokens
	}
	if m == modeStructured {
		b["response_format"] = map[string]any{"type": "json_object"}
	}
	return b
}
