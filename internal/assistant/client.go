package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

const maxBody = 4 << 20

func displayName(p model.AiProvider) string {
	if p == model.ProviderChatGPT {
		return "ChatGPT"
	}
	return "Claude"
}

func errUnreadable(p model.AiProvider) error {
	return fmt.Errorf("%s sent an answer Bersihin could not read. Please try again.", displayName(p))
}

func errRefused(p model.AiProvider) error {
	return fmt.Errorf("%s could not help with that request. Try asking in a different way.", displayName(p))
}

var errCutOff = errors.New("The answer got cut off. Try a narrower question.")

// statusError maps an HTTP status to a message the user can act on.
func statusError(p model.AiProvider, status int) error {
	name := displayName(p)
	keyName := name
	if p == model.ProviderChatGPT {
		keyName = "OpenAI"
	}
	switch {
	case status == http.StatusUnauthorized:
		return fmt.Errorf("Your %s API key was rejected. Check it in Settings.", keyName)
	case status == http.StatusForbidden:
		return fmt.Errorf("Your %s API key does not have access to this model.", keyName)
	case status == http.StatusTooManyRequests:
		if p == model.ProviderChatGPT {
			return errors.New("ChatGPT is busy or your OpenAI quota is used up. Try again later.")
		}
		return errors.New("Claude is getting too many requests right now. Wait a minute and try again.")
	case status == http.StatusBadRequest:
		return fmt.Errorf("%s could not process this request. Try a shorter question.", name)
	case status >= 500:
		return fmt.Errorf("%s is having trouble right now. Please try again shortly.", name)
	}
	return fmt.Errorf("%s returned an error (%d). Please try again.", name, status)
}

// transportError maps a failed round trip.
func transportError(ctx context.Context, p model.AiProvider, err error) error {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err() // the page went away
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("%s took too long to answer. Please try again.", displayName(p))
	}
	return fmt.Errorf("Could not reach %s. Check your internet connection.", displayName(p))
}

// post sends a JSON body and returns the response body of a 2xx answer. The
// key travels only in the headers and never appears in an error.
func (s *Service) post(ctx context.Context, p model.AiProvider, url string, headers map[string]string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, config.AssistantTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("The assistant is not set up correctly.")
	}
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// A copy that never follows a redirect: the key must only go to the
	// endpoint it was meant for.
	client := http.Client{Timeout: config.AssistantTimeout}
	if s.HTTP != nil {
		client = *s.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, transportError(ctx, p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil, statusError(p, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError(ctx, p, err)
	}
	return b, nil
}

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

type request struct{ apiKey, system, user string }

// askClaude calls the Anthropic Messages API with a JSON schema output
// format, and returns the JSON text of the answer. Claude Opus 5.5 always
// thinks, so no thinking parameter is sent; thinking blocks are skipped.
func (s *Service) askClaude(ctx context.Context, r request) ([]byte, error) {
	url := s.AnthropicURL
	if url == "" {
		url = config.AnthropicURL
	}
	body := map[string]any{
		"model":      config.ClaudeModel,
		"max_tokens": config.AssistantMaxTokens,
		"system":     r.system,
		"messages":   []map[string]any{{"role": "user", "content": r.user}},
		"output_config": map[string]any{
			"format": map[string]any{"type": "json_schema", "schema": adviceSchema},
		},
	}
	raw, err := s.post(ctx, model.ProviderClaude, url, map[string]string{
		"x-api-key": r.apiKey, "anthropic-version": config.AnthropicVersion,
	}, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return nil, errUnreadable(model.ProviderClaude)
	}
	switch resp.StopReason {
	case "refusal":
		return nil, errRefused(model.ProviderClaude)
	case "max_tokens":
		return nil, errCutOff
	}
	var text strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	if text.Len() == 0 {
		return nil, errUnreadable(model.ProviderClaude)
	}
	return []byte(text.String()), nil
}

// askOpenAI calls the Chat Completions API in JSON mode.
func (s *Service) askOpenAI(ctx context.Context, r request) ([]byte, error) {
	url := s.OpenAIURL
	if url == "" {
		url = config.OpenAIURL
	}
	body := map[string]any{
		"model":                 config.OpenAIModel,
		"response_format":       map[string]any{"type": "json_object"},
		"max_completion_tokens": config.AssistantMaxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": r.system},
			{"role": "user", "content": r.user},
		},
	}
	raw, err := s.post(ctx, model.ProviderChatGPT, url, map[string]string{"authorization": "Bearer " + r.apiKey}, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &resp) != nil || len(resp.Choices) == 0 {
		return nil, errUnreadable(model.ProviderChatGPT)
	}
	c := resp.Choices[0]
	switch {
	case c.Message.Refusal != "", c.FinishReason == "content_filter":
		return nil, errRefused(model.ProviderChatGPT)
	case c.FinishReason == "length":
		return nil, errCutOff
	case strings.TrimSpace(c.Message.Content) == "":
		return nil, errUnreadable(model.ProviderChatGPT)
	}
	return []byte(c.Message.Content), nil
}
