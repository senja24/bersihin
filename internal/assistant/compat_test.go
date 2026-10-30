package assistant

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

const compatKey = "local-key-123"

const goodAdvice = `{"answer":"Clear the caches.","suggestions":[{"path":"~/Library/Caches","verdict":"safe","reason":"Regenerable."}]}`

// compat builds a service for a compatible provider that talks to base.
func compat(p model.AiProvider, base, mdl, key string) *Service {
	return &Service{
		Home:     home,
		Provider: func() model.AiProvider { return p },
		Key:      func(model.AiProvider) (string, error) { return key, nil },
		Endpoint: func(model.AiProvider) model.AiEndpoint { return model.AiEndpoint{BaseUrl: base, Model: mdl} },
		Version:  func() string { return "1.2.3" },
	}
}

func openAIReply(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
	return string(b)
}

func claudeReply(text string) string {
	b, _ := json.Marshal(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": text}}})
	return string(b)
}

// recorder is a server that logs every request and answers from a script: the
// n-th request gets script[n], the last entry repeats.
type recorder struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

type recorded struct {
	path    string
	headers http.Header
	body    map[string]any
}

type answer struct {
	status int
	body   string
	header map[string]string
}

func newRecorder(t *testing.T, script ...answer) *recorder {
	t.Helper()
	r := &recorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		r.mu.Lock()
		i := len(r.reqs)
		r.reqs = append(r.reqs, recorded{req.URL.Path, req.Header.Clone(), body})
		r.mu.Unlock()
		a := script[min(i, len(script)-1)]
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(a.status)
		io.WriteString(w, a.body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func (r *recorder) at(i int) recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[i]
}

func ok200(body string) answer { return answer{status: 200, body: body} }

func TestCompatRequests(t *testing.T) {
	t.Run("openai-compatible", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply(goodAdvice)))
		s := compat(model.ProviderOpenAICompat, rec.srv.URL+"/api/v1", "llama-3", compatKey)
		adv, err := s.Ask(context.Background(), "What can I delete from /Users/agus/Library?", sampleContext())
		if err != nil {
			t.Fatal(err)
		}
		if adv.Provider != model.ProviderOpenAICompat || adv.Suggestions[0].Verdict != model.VerdictSafe {
			t.Fatalf("advice = %+v", adv)
		}
		r := rec.at(0)
		if r.path != "/api/v1/chat/completions" {
			t.Errorf("path = %s", r.path)
		}
		h := r.headers
		if h.Get("authorization") != "Bearer "+compatKey || h.Get("x-api-key") != "" || h.Get("content-type") != "application/json" ||
			h.Get("user-agent") != "Bersihin/1.2.3" || h.Get("cookie") != "" {
			t.Errorf("headers = %v", h)
		}
		b := r.body
		if b["model"] != "llama-3" || b["max_tokens"] != float64(config.AssistantMaxTokens) || b["stream"] != false ||
			b["response_format"].(map[string]any)["type"] != "json_object" {
			t.Errorf("body = %v", b)
		}
		if _, has := b["max_completion_tokens"]; has {
			t.Error("max_completion_tokens sent to a compatible server")
		}
		if msgs := b["messages"].([]any); msgs[0].(map[string]any)["content"] != SystemPrompt {
			t.Error("system prompt changed in structured mode")
		}
	})
	t.Run("claude-compatible", func(t *testing.T) {
		rec := newRecorder(t, ok200(claudeReply(goodAdvice)))
		s := compat(model.ProviderClaudeCompat, rec.srv.URL, "claude-x", compatKey)
		if _, err := s.Ask(context.Background(), "q", sampleContext()); err != nil {
			t.Fatal(err)
		}
		r := rec.at(0)
		if r.path != "/v1/messages" {
			t.Errorf("path = %s", r.path)
		}
		h := r.headers
		if h.Get("x-api-key") != compatKey || h.Get("anthropic-version") != config.AnthropicVersion || h.Get("authorization") != "Bearer "+compatKey {
			t.Errorf("headers = %v", h)
		}
		b := r.body
		if b["model"] != "claude-x" || b["max_tokens"] != float64(config.AssistantMaxTokens) || b["system"] != SystemPrompt {
			t.Errorf("body = %v", b)
		}
		if f := b["output_config"].(map[string]any)["format"].(map[string]any); f["type"] != "json_schema" {
			t.Errorf("output_config = %v", f)
		}
	})
}

// The official requests are byte for byte what they were before compatible
// providers existed.
func TestOfficialRequestsUnchanged(t *testing.T) {
	const system, user = "SYS", "USR"
	oldClaude := map[string]any{
		"model": config.ClaudeModel, "max_tokens": config.AssistantMaxTokens, "system": system,
		"messages":      []map[string]any{{"role": "user", "content": user}},
		"output_config": map[string]any{"format": map[string]any{"type": "json_schema", "schema": adviceSchema}},
	}
	oldOpenAI := map[string]any{
		"model": config.OpenAIModel, "response_format": map[string]any{"type": "json_object"},
		"max_completion_tokens": config.AssistantMaxTokens,
		"messages":              []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": user}},
	}
	s := &Service{}
	for _, tc := range []struct {
		p       model.AiProvider
		old     map[string]any
		url     string
		headers map[string]string
	}{
		{model.ProviderClaude, oldClaude, config.AnthropicURL, map[string]string{"x-api-key": "K", "anthropic-version": config.AnthropicVersion}},
		{model.ProviderChatGPT, oldOpenAI, config.OpenAIURL, map[string]string{"authorization": "Bearer K"}},
	} {
		t.Run(string(tc.p), func(t *testing.T) {
			tg, ce := s.target(tc.p, model.AiEndpoint{}, "K")
			if ce != nil {
				t.Fatal(ce)
			}
			got, _ := json.Marshal(tg.body(system, user, config.AssistantMaxTokens, modeStructured))
			want, _ := json.Marshal(tc.old)
			if string(got) != string(want) {
				t.Errorf("body changed:\n got %s\nwant %s", got, want)
			}
			if tg.url != tc.url || len(tg.headers) != len(tc.headers) {
				t.Errorf("url %s headers %v", tg.url, tg.headers)
			}
			for k, v := range tc.headers {
				if tg.headers[k] != v {
					t.Errorf("header %s = %q", k, tg.headers[k])
				}
			}
		})
	}
}

func TestStructuredOutputLadder(t *testing.T) {
	fieldErr := answer{status: 400, body: `{"error":{"message":"Unsupported parameter: 'response_format'"}}`}
	tests := []struct {
		name     string
		provider model.AiProvider
		script   []answer
		wantReqs int
		wantOK   bool
		wantCode string
	}{
		{"400 naming response_format retries plain", model.ProviderOpenAICompat, []answer{fieldErr, ok200(openAIReply(goodAdvice))}, 2, true, ""},
		{"400 without a name retries plain", model.ProviderOpenAICompat, []answer{{status: 400, body: `bad`}, ok200(openAIReply(goodAdvice))}, 2, true, ""},
		{"422 retries plain (claude)", model.ProviderClaudeCompat, []answer{{status: 422, body: ``}, ok200(claudeReply(goodAdvice))}, 2, true, ""},
		{"404 naming the field retries", model.ProviderOpenAICompat, []answer{{status: 404, body: `no route for response_format`}, ok200(openAIReply(goodAdvice))}, 2, true, ""},
		{"404 without the field does not", model.ProviderOpenAICompat, []answer{{status: 404, body: `page not found`}}, 1, false, codeNotFound},
		{"400 unknown model does not retry", model.ProviderOpenAICompat, []answer{{status: 400, body: `{"error":"model 'x' not found"}`}}, 1, false, codeModel},
		{"retried once only", model.ProviderOpenAICompat, []answer{fieldErr, fieldErr, ok200(openAIReply(goodAdvice))}, 2, false, codeOther},
		{"401 once", model.ProviderOpenAICompat, []answer{{status: 401}}, 1, false, codeAuth},
		{"403 once", model.ProviderClaudeCompat, []answer{{status: 403}}, 1, false, codeAuth},
		{"429 once", model.ProviderOpenAICompat, []answer{{status: 429}}, 1, false, codeRate},
		{"500 once", model.ProviderOpenAICompat, []answer{{status: 500}}, 1, false, codeServer},
		{"503 once", model.ProviderClaudeCompat, []answer{{status: 503}}, 1, false, codeServer},
		{"200 once", model.ProviderOpenAICompat, []answer{ok200(openAIReply(goodAdvice))}, 1, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder(t, tc.script...)
			s := compat(tc.provider, rec.srv.URL, "m", compatKey)
			res, err := s.TestConnection(context.Background(), tc.provider, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.OK != tc.wantOK || (!tc.wantOK && res.Code != tc.wantCode) {
				t.Fatalf("result = %+v", res)
			}
			if rec.count() != tc.wantReqs {
				t.Fatalf("%d requests, want %d", rec.count(), tc.wantReqs)
			}
			if tc.wantReqs == 2 && tc.wantOK {
				first, second := rec.at(0).body, rec.at(1).body
				_, a := first["response_format"]
				_, b := first["output_config"]
				_, c := second["response_format"]
				_, d := second["output_config"]
				if !(a || b) || c || d {
					t.Errorf("structured option: first %v second %v", first, second)
				}
				if sys := systemOf(second); sys != SystemPrompt+plainSuffix {
					t.Errorf("second system prompt = %q", sys)
				}
				if res.Mode != "plain" {
					t.Errorf("mode = %q", res.Mode)
				}
			}
		})
	}
}

func systemOf(body map[string]any) string {
	if s, ok := body["system"].(string); ok {
		return s
	}
	return body["messages"].([]any)[0].(map[string]any)["content"].(string)
}

func TestWorkingModeIsRemembered(t *testing.T) {
	rec := newRecorder(t,
		answer{status: 400, body: `response_format is not supported`},
		ok200(openAIReply(goodAdvice)), ok200(openAIReply(goodAdvice)))
	s := compat(model.ProviderOpenAICompat, rec.srv.URL, "m", compatKey)
	for range 2 {
		if _, err := s.Ask(context.Background(), "q", sampleContext()); err != nil {
			t.Fatal(err)
		}
	}
	if rec.count() != 3 {
		t.Fatalf("%d requests, want 3 (structured, plain, plain)", rec.count())
	}
	if _, has := rec.at(2).body["response_format"]; has || systemOf(rec.at(2).body) != SystemPrompt+plainSuffix {
		t.Errorf("third request was not plain: %v", rec.at(2).body)
	}
	// another model is another entry
	s.Endpoint = func(model.AiProvider) model.AiEndpoint {
		return model.AiEndpoint{BaseUrl: rec.srv.URL, Model: "other"}
	}
	s.Ask(context.Background(), "q", sampleContext())
	if _, has := rec.at(3).body["response_format"]; !has {
		t.Error("a new model should start structured")
	}
}

func TestRedirectNotFollowedForCompatible(t *testing.T) {
	target := newRecorder(t, ok200(openAIReply(goodAdvice)))
	for _, status := range []int{301, 302, 307, 308} {
		redir := newRecorder(t, answer{status: status, header: map[string]string{"Location": target.srv.URL + "/v1/chat/completions"}})
		s := compat(model.ProviderOpenAICompat, redir.srv.URL, "m", compatKey)
		s.HTTP = &http.Client{} // a client that would follow
		res, _ := s.TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
		if res.OK || res.Code != codeRedirect || redir.count() != 1 {
			t.Errorf("%d: result %+v, %d requests", status, res, redir.count())
		}
		if _, err := s.Ask(context.Background(), "q", model.AiContext{}); err == nil || err.Error() != codeMessages[codeRedirect] {
			t.Errorf("%d: Ask err = %v", status, err)
		}
	}
	if target.count() != 0 {
		t.Fatal("the redirect target was contacted (and would have received the key)")
	}
}

func TestExtractJSON(t *testing.T) {
	const obj = `{"answer":"hi","suggestions":[]}`
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"plain", obj, obj, false},
		{"whitespace", "\n  " + obj + " \n", obj, false},
		{"fenced json", "```json\n" + obj + "\n```", obj, false},
		{"fenced bare", "```\n" + obj + "\n```\n", obj, false},
		{"fence unclosed", "```json\n" + obj, obj, false},
		{"prose around", "Sure! Here you go:\n" + obj + "\nHope that helps.", obj, false},
		{"prose and fence", "Here:\n```json\n" + obj + "\n```\nbye", obj, false},
		{"think then json", "<think>I should {consider} this</think>\n" + obj, obj, false},
		{"think multiline then fence", "<think>\nline1\nline2\n</think>\n```json\n" + obj + "\n```", obj, false},
		{"think never closed", "<think>hmm " + obj, "", true},
		{"braces in strings", `{"answer":"use {x} and } and \" {","suggestions":[]}`, `{"answer":"use {x} and } and \" {","suggestions":[]}`, false},
		{"nested", `{"answer":"a","suggestions":[{"path":"p","verdict":"safe","reason":"r"}]}`, `{"answer":"a","suggestions":[{"path":"p","verdict":"safe","reason":"r"}]}`, false},
		{"junk braces before", "use {curly} like so: " + obj, obj, false},
		{"truncated", `{"answer":"hi","suggestions":[{"path":"p"`, "", true},
		{"truncated after inner object", `{"answer":"hi","suggestions":[{"path":"p","verdict":"safe","reason":"r"}`, "", true},
		{"two objects takes first", obj + "\n" + `{"answer":"second","suggestions":[]}`, obj, false},
		{"top-level array", `[` + obj + `]`, "", true},
		{"top-level array in fence", "```json\n[" + obj + "]\n```", "", true},
		{"empty", "", "", true},
		{"spaces only", "  \n ", "", true},
		{"no json", "I cannot help with that.", "", true},
		{"invalid json", `{answer: hi}`, "", true},
		{"huge", obj + strings.Repeat(" ", maxExtract), "", true},
		{"bom", "\ufeff" + obj, obj, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractJSON(tc.in)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %q, err %v; want %q (err %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestExtractAdviceIsStrict(t *testing.T) {
	for name, in := range map[string]string{
		"unknown top-level key":   `{"answer":"hi","suggestions":[],"extra":1}`,
		"tool call lookalike":     `{"name":"search","arguments":{}}`,
		"example inside prose":    `Example: {"path":"~/x","verdict":"safe","reason":"r"}`,
		"unknown key after fence": "```json\n" + `{"answer":"a","confidence":0.9}` + "\n```",
	} {
		if b, err := extractAdvice(in); err == nil {
			t.Errorf("%s: accepted %s", name, b)
		}
	}
	if b, err := extractAdvice(`{"answer":"only an answer"}`); err != nil || string(b) != `{"answer":"only an answer"}` {
		t.Errorf("answer only: %s %v", b, err)
	}
}

func TestCompatAskExtractsAndStillDowngrades(t *testing.T) {
	text := "<think>The user asks...</think>\nHere is the result:\n```json\n" +
		`{"answer":"Clear caches; keep the film.","suggestions":[` +
		`{"path":"~/Library/Caches","verdict":"safe","reason":"Regenerable."},` +
		`{"path":"~/Movies/film.mkv","verdict":"safe","reason":"Model overreach."},` +
		`{"path":"~/Nowhere","verdict":"safe","reason":"Invented."}]}` + "\n```"
	for _, p := range []model.AiProvider{model.ProviderOpenAICompat, model.ProviderClaudeCompat} {
		body := openAIReply(text)
		if p == model.ProviderClaudeCompat {
			body = claudeReply(text)
		}
		rec := newRecorder(t, ok200(body))
		adv, err := compat(p, rec.srv.URL, "m", compatKey).Ask(context.Background(), "q", sampleContext())
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		got := map[string]model.Verdict{}
		for _, s := range adv.Suggestions {
			got[s.Path] = s.Verdict
		}
		if got[home+"/Library/Caches"] != model.VerdictSafe || got[home+"/Movies/film.mkv"] != model.VerdictReview || got[home+"/Nowhere"] != model.VerdictReview {
			t.Errorf("%s: verdicts = %v", p, got)
		}
	}
}

func TestCompatAskErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"unknown key", openAIReply(`{"answer":"x","suggestions":[],"evil":1}`), "The OpenAI-compatible server sent an answer Bersihin could not read. Please try again."},
		{"prose only", openAIReply(`I cannot do that`), "The OpenAI-compatible server sent an answer Bersihin could not read. Please try again."},
		{"cut off", `{"choices":[{"finish_reason":"length","message":{"content":"{\"answer\":"}}]}`, "The answer got cut off. Try a narrower question."},
		{"refusal", `{"choices":[{"message":{"refusal":"no"}}]}`, "The OpenAI-compatible server could not help with that request. Try asking in a different way."},
		{"not an envelope", `<html>`, "The OpenAI-compatible server sent an answer Bersihin could not read. Please try again."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder(t, ok200(tc.body))
			_, err := compat(model.ProviderOpenAICompat, rec.srv.URL, "m", compatKey).Ask(context.Background(), "q", model.AiContext{})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCompatAskNotSetUp(t *testing.T) {
	rec := newRecorder(t, ok200(openAIReply(goodAdvice)))
	tests := []struct {
		name, base, model, key, want string
	}{
		{"no address", "", "m", "k", "Set up your OpenAI-compatible provider in Settings first"},
		{"no model", rec.srv.URL, "", "k", "Set up your OpenAI-compatible provider in Settings first"},
		{"no key", rec.srv.URL, "m", "", "Add the API key for your OpenAI-compatible provider in Settings to use the assistant."},
		{"stored address no longer valid", "http://192.168.1.5/v1", "m", "k", "only allowed for this computer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compat(model.ProviderOpenAICompat, tc.base, tc.model, tc.key).Ask(context.Background(), "q", model.AiContext{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if rec.count() != 0 {
		t.Fatal("a request was sent")
	}
}

func TestPostTooBig(t *testing.T) {
	big := openAIReply(strings.Repeat("a", config.AssistantMaxResponse))
	rec := newRecorder(t, ok200(big))
	res, _ := compat(model.ProviderOpenAICompat, rec.srv.URL, "m", compatKey).TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	if res.OK || res.Code != codeTooBig {
		t.Fatalf("result = %+v", res)
	}
	// exactly at the limit is accepted
	body := openAIReply("x")
	body += strings.Repeat(" ", config.AssistantMaxResponse-len(body))
	rec2 := newRecorder(t, ok200(body))
	res, _ = compat(model.ProviderOpenAICompat, rec2.srv.URL, "m", compatKey).TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	if !res.OK {
		t.Fatalf("a body of exactly the limit failed: %+v", res)
	}
	// an official provider reports it as an unreadable answer
	s := service(rec.srv.URL, model.ProviderClaude, claudeKey)
	if _, err := s.Ask(context.Background(), "q", model.AiContext{}); err == nil || err.Error() != "Claude sent an answer Bersihin could not read. Please try again." {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorBodiesNeverSurface(t *testing.T) {
	bodies := []string{
		`{"error":"invalid key ` + compatKey + `"}`,
		strings.Repeat("x", 10000) + compatKey, // beyond the 4 KiB that are read
		`Authorization: Bearer ` + compatKey,
	}
	for _, status := range []int{400, 401, 403, 404, 418, 429, 500, 502} {
		for _, body := range bodies {
			rec := newRecorder(t, answer{status: status, body: body})
			for _, p := range []model.AiProvider{model.ProviderOpenAICompat, model.ProviderClaudeCompat} {
				s := compat(p, rec.srv.URL, "m", compatKey)
				res, _ := s.TestConnection(context.Background(), p, nil)
				blob, _ := json.Marshal(res)
				_, err := s.Ask(context.Background(), "q", model.AiContext{})
				if strings.Contains(string(blob), compatKey) || strings.Contains(string(blob), "xxxx") || err == nil || strings.Contains(err.Error(), compatKey) || strings.Contains(err.Error(), "xxxx") {
					t.Fatalf("status %d: result %s, err %v", status, blob, err)
				}
			}
		}
	}
	// an official error body
	rec := newRecorder(t, answer{status: 401, body: claudeKey})
	_, err := service(rec.srv.URL, model.ProviderClaude, claudeKey).Ask(context.Background(), "q", model.AiContext{})
	if err == nil || strings.Contains(err.Error(), claudeKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorBodyReadIsCapped(t *testing.T) {
	var sent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		chunk := []byte(strings.Repeat("y", 1024))
		for range 4096 { // 4 MiB offered
			if _, err := w.Write(chunk); err != nil {
				return
			}
			sent.Add(1024)
		}
	}))
	defer srv.Close()
	res, _ := compat(model.ProviderOpenAICompat, srv.URL, "m", compatKey).TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	if res.Code != codeServer {
		t.Fatalf("result = %+v", res)
	}
}

func TestTestConnectionCodes(t *testing.T) {
	const p = model.ProviderOpenAICompat
	run := func(t *testing.T, s *Service, draft *model.AiEndpointPatch) model.AiTestResult {
		t.Helper()
		res, err := s.TestConnection(context.Background(), p, draft)
		if err != nil {
			t.Fatal(err)
		}
		if res.Ms < 0 {
			t.Errorf("ms = %d", res.Ms)
		}
		if res.Message == "" || res.Code == "" {
			t.Errorf("result = %+v", res)
		}
		return res
	}

	replies := []struct {
		name     string
		ans      answer
		wantCode string
		message  string
	}{
		{"auth 401", answer{status: 401}, codeAuth, codeMessages[codeAuth]},
		{"auth 403", answer{status: 403}, codeAuth, codeMessages[codeAuth]},
		{"not found", answer{status: 404, body: "404 page not found"}, codeNotFound, "Wrong address or path. Try adding or removing /v1."},
		{"model openai style", answer{status: 404, body: `{"error":{"message":"The model ` + "`x`" + ` does not exist","code":"model_not_found"}}`}, codeModel, codeMessages[codeModel]},
		{"model anthropic style", answer{status: 404, body: `{"type":"error","error":{"type":"not_found_error","message":"model: x"}}`}, codeModel, codeMessages[codeModel]},
		{"model ollama style", answer{status: 404, body: `{"error":"model 'x' not found"}`}, codeModel, codeMessages[codeModel]},
		{"rate", answer{status: 429}, codeRate, codeMessages[codeRate]},
		{"server 500", answer{status: 500}, codeServer, codeMessages[codeServer]},
		{"server 529", answer{status: 529}, codeServer, codeMessages[codeServer]},
		{"redirect", answer{status: 302, header: map[string]string{"Location": "https://elsewhere.example/"}}, codeRedirect, codeMessages[codeRedirect]},
		{"other 418", answer{status: 418}, codeOther, codeMessages[codeOther]},
		{"not json html", ok200("<html>login</html>"), codeNotJSON, codeMessages[codeNotJSON]},
		{"not json empty content", ok200(openAIReply("  ")), codeNotJSON, codeMessages[codeNotJSON]},
		{"not json no choices", ok200(`{"choices":[]}`), codeNotJSON, codeMessages[codeNotJSON]},
		{"ok", ok200(openAIReply("pong")), codeOK, codeMessages[codeOK]},
		{"ok even when cut off", ok200(`{"choices":[{"finish_reason":"length","message":{"content":"po"}}]}`), codeOK, codeMessages[codeOK]},
	}
	for _, tc := range replies {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder(t, tc.ans)
			res := run(t, compat(p, rec.srv.URL+"/v1", "m", compatKey), nil)
			if res.Code != tc.wantCode || res.Message != tc.message || res.OK != (tc.wantCode == codeOK) {
				t.Fatalf("result = %+v", res)
			}
			// detail: the status and host+path only
			host := strings.TrimPrefix(rec.srv.URL, "http://") + "/v1/chat/completions"
			switch {
			case tc.wantCode == codeOK:
				if res.Detail != "" || res.Mode != "structured" {
					t.Errorf("detail %q mode %q", res.Detail, res.Mode)
				}
			case tc.ans.status >= 300 && tc.ans.status != 200:
				if want := "HTTP " + strconv.Itoa(tc.ans.status) + " at " + host; res.Detail != want {
					t.Errorf("detail = %q, want %q", res.Detail, want)
				}
			case res.Detail != host:
				t.Errorf("detail = %q, want %q", res.Detail, host)
			}
			if strings.Contains(res.Detail+res.Message, "login") || strings.Contains(res.Detail+res.Message, "elsewhere") {
				t.Error("server text surfaced")
			}
		})
	}

	t.Run("claude-compatible ok", func(t *testing.T) {
		rec := newRecorder(t, ok200(claudeReply("pong")))
		res, err := compat(model.ProviderClaudeCompat, rec.srv.URL, "m", compatKey).TestConnection(context.Background(), model.ProviderClaudeCompat, nil)
		if err != nil || !res.OK || res.Mode != "structured" {
			t.Fatalf("%+v %v", res, err)
		}
		b := rec.at(0).body
		if b["max_tokens"] != float64(config.AssistantTestMaxTokens) || b["system"] != SystemPrompt || !strings.Contains(b["messages"].([]any)[0].(map[string]any)["content"].(string), "Question: ping") {
			t.Errorf("body = %v", b)
		}
		if c := b["messages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(c, "<bersihin_data>\n{}\n</bersihin_data>") {
			t.Errorf("data block not empty: %s", c)
		}
	})

	t.Run("no address", func(t *testing.T) {
		if res := run(t, compat(p, "", "m", compatKey), nil); res.Code != codeBadURL {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("stored address invalid", func(t *testing.T) {
		if res := run(t, compat(p, "http://192.168.1.5/v1", "m", compatKey), nil); res.Code != codeBadURL {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("draft address invalid", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply("pong")))
		for _, bad := range []string{"", "http://192.168.1.5", "ftp://x", "https://u:p@x.example", rec.srv.URL + "?k=v", rec.srv.URL + ".evil.com"} {
			res := run(t, compat(p, rec.srv.URL, "m", compatKey), &model.AiEndpointPatch{BaseUrl: &bad})
			if res.Code != codeBadURL {
				t.Errorf("%q: %+v", bad, res)
			}
		}
		if rec.count() != 0 {
			t.Fatal("a request was sent")
		}
	})
	t.Run("no model", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply("pong")))
		if res := run(t, compat(p, rec.srv.URL, "", compatKey), nil); res.Code != codeNoModel {
			t.Fatalf("%+v", res)
		}
		empty := " "
		if res := run(t, compat(p, rec.srv.URL, "m", compatKey), &model.AiEndpointPatch{Model: &empty}); res.Code != codeNoModel {
			t.Fatalf("draft: %+v", res)
		}
		if rec.count() != 0 {
			t.Fatal("a request was sent")
		}
	})
	t.Run("no key", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply("pong")))
		if res := run(t, compat(p, rec.srv.URL, "m", ""), nil); res.Code != codeNoKey || rec.count() != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("keychain error", func(t *testing.T) {
		s := compat(p, "https://example.com", "m", "")
		s.Key = func(model.AiProvider) (string, error) { return "", errors.New("The saved API key could not be read") }
		if res := run(t, s, nil); res.Code != codeOther || res.Message != "The saved API key could not be read" {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("unknown provider", func(t *testing.T) {
		res, err := compat(p, "", "", "k").TestConnection(context.Background(), model.AiProvider("x"), nil)
		if err != nil || res.OK || res.Code != codeOther {
			t.Fatalf("%+v %v", res, err)
		}
	})

	t.Run("dns", func(t *testing.T) {
		s := compat(p, "http://nope.localhost:9", "m", compatKey)
		s.Lookup = func(context.Context, string) ([]netip.Addr, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "nope.localhost", IsNotFound: true}
		}
		if res := run(t, s, nil); res.Code != codeDNS || res.Message != codeMessages[codeDNS] {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("refused", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		if res := run(t, compat(p, url, "m", compatKey), nil); res.Code != codeRefused {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("tls untrusted certificate", func(t *testing.T) {
		var hit atomic.Bool
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hit.Store(true)
			io.WriteString(w, openAIReply("pong"))
		}))
		defer srv.Close()
		if res := run(t, compat(p, srv.URL, "m", compatKey), nil); res.Code != codeTLS || res.Message != codeMessages[codeTLS] {
			t.Fatalf("%+v", res)
		}
		if hit.Load() {
			t.Fatal("the request reached a server with an untrusted certificate")
		}
	})
	t.Run("tls trusted certificate", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, openAIReply("pong"))
		}))
		defer srv.Close()
		s := compat(p, srv.URL, "m", compatKey)
		s.HTTP = srv.Client()
		if res := run(t, s, nil); !res.OK {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("http answered on an https address", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer srv.Close()
		if res := run(t, compat(p, "https://"+strings.TrimPrefix(srv.URL, "http://"), "m", compatKey), nil); res.Code != codeTLS {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		s := compat(p, srv.URL, "m", compatKey)
		s.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
		res := run(t, s, nil)
		if res.Code != codeTimeout {
			t.Fatalf("%+v", res)
		}
		// never retried
		if _, err := s.Ask(context.Background(), "q", model.AiContext{}); err == nil || err.Error() != codeMessages[codeTimeout] {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("page goes away", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(30 * time.Millisecond); cancel() }()
		_, err := compat(p, srv.URL, "m", compatKey).TestConnection(ctx, p, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTestConnectionDraft(t *testing.T) {
	const p = model.ProviderOpenAICompat
	t.Run("same host new path and model use the saved key", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply("pong")))
		s := compat(p, rec.srv.URL, "old-model", compatKey)
		base, mdl := rec.srv.URL+"/openai/v1/", " new-model "
		res, err := s.TestConnection(context.Background(), p, &model.AiEndpointPatch{BaseUrl: &base, Model: &mdl})
		if err != nil || !res.OK {
			t.Fatalf("%+v %v", res, err)
		}
		r := rec.at(0)
		if r.path != "/openai/v1/chat/completions" || r.body["model"] != "new-model" || r.headers.Get("authorization") != "Bearer "+compatKey {
			t.Errorf("path %s body %v headers %v", r.path, r.body, r.headers)
		}
	})
	t.Run("a different host is refused and gets no key", func(t *testing.T) {
		saved := newRecorder(t, ok200(openAIReply("pong")))
		evil := newRecorder(t, ok200(openAIReply("pong")))
		s := compat(p, saved.srv.URL, "m", compatKey)
		for _, u := range []string{
			evil.srv.URL,
			evil.srv.URL + "/v1",
			strings.Replace(saved.srv.URL, "127.0.0.1", "localhost", 1), // same machine, another name
		} {
			res, err := s.TestConnection(context.Background(), p, &model.AiEndpointPatch{BaseUrl: &u})
			if err != nil || res.OK || res.Code != codeBadURL || !strings.Contains(res.Message, "Save") {
				t.Errorf("%s: %+v %v", u, res, err)
			}
		}
		if evil.count() != 0 || saved.count() != 0 {
			t.Fatalf("requests sent: evil %d saved %d", evil.count(), saved.count())
		}
	})
	t.Run("nothing saved yet, so any draft host is refused", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply("pong")))
		s := compat(p, "", "m", compatKey)
		res, _ := s.TestConnection(context.Background(), p, &model.AiEndpointPatch{BaseUrl: &rec.srv.URL})
		if res.OK || res.Code != codeBadURL || rec.count() != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("settings change between read and send", func(t *testing.T) {
		rec := newRecorder(t, ok200(openAIReply(goodAdvice)))
		other := newRecorder(t, ok200(openAIReply(goodAdvice)))
		var calls atomic.Int32
		s := compat(model.ProviderOpenAICompat, rec.srv.URL, "m", compatKey)
		s.Endpoint = func(model.AiProvider) model.AiEndpoint {
			if calls.Add(1) > 1 { // the user saved another host meanwhile
				return model.AiEndpoint{BaseUrl: other.srv.URL, Model: "m"}
			}
			return model.AiEndpoint{BaseUrl: rec.srv.URL, Model: "m"}
		}
		if _, err := s.Ask(context.Background(), "q", model.AiContext{}); err == nil {
			t.Fatal("expected an error")
		}
		if rec.count() != 0 || other.count() != 0 {
			t.Fatal("the key was sent although the host changed")
		}
	})
}

func TestOfficialProviderTestConnection(t *testing.T) {
	rec := newRecorder(t, ok200(claudeReply("pong")))
	s := service(rec.srv.URL, model.ProviderClaude, claudeKey)
	base := "https://ignored.example"
	res, err := s.TestConnection(context.Background(), model.ProviderClaude, &model.AiEndpointPatch{BaseUrl: &base})
	if err != nil || !res.OK || res.Mode != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if rec.at(0).headers.Get("x-api-key") != claudeKey || rec.at(0).body["model"] != config.ClaudeModel {
		t.Errorf("request = %+v", rec.at(0))
	}
}

func TestLoopbackDial(t *testing.T) {
	srv := newRecorder(t, ok200(openAIReply("pong")))
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.srv.URL, "http://"))
	lookups := map[string][]netip.Addr{
		"ollama.localhost":   {netip.MustParseAddr("127.0.0.1")},
		"hosts.localhost":    {netip.MustParseAddr("::ffff:127.0.0.1")},
		"remap.localhost":    {netip.MustParseAddr("192.168.1.50")},
		"mixed.localhost":    {netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.9")},
		"metadata.localhost": {netip.MustParseAddr("169.254.169.254")},
		"empty.localhost":    nil,
	}
	var dialed []string
	var mu sync.Mutex
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		dialed = append(dialed, host)
		mu.Unlock()
		return lookups[host], nil
	}
	const p = model.ProviderOpenAICompat
	for host, want := range map[string]string{
		"ollama.localhost": codeOK, "hosts.localhost": codeOK,
		"remap.localhost": codeBadURL, "mixed.localhost": codeBadURL, "metadata.localhost": codeBadURL, "empty.localhost": codeBadURL,
	} {
		t.Run(host, func(t *testing.T) {
			before := srv.count()
			s := compat(p, "http://"+host+":"+port, "m", compatKey)
			s.Lookup = lookup
			res, err := s.TestConnection(context.Background(), p, nil)
			if err != nil || res.Code != want {
				t.Fatalf("%+v %v", res, err)
			}
			if got := srv.count() - before; (want == codeOK) != (got == 1) {
				t.Fatalf("%d requests reached the server", got)
			}
		})
	}
	// a literal IP needs no lookup
	dialed = nil
	s := compat(p, srv.srv.URL, "m", compatKey)
	s.Lookup = lookup
	if res, _ := s.TestConnection(context.Background(), p, nil); !res.OK || len(dialed) != 0 {
		t.Fatalf("%+v lookups %v", res, dialed)
	}
}

func TestLoopbackDialDirect(t *testing.T) {
	dial := loopbackDial(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	if c, err := dial(context.Background(), "tcp", "example.localhost:80"); !errors.Is(err, errNotLoopback) || c != nil {
		t.Fatalf("err = %v", err)
	}
	if c, err := dial(context.Background(), "tcp", "8.8.8.8:80"); !errors.Is(err, errNotLoopback) || c != nil {
		t.Fatalf("literal: err = %v", err)
	}
	if _, err := dial(context.Background(), "tcp", "noport"); err == nil {
		t.Fatal("bad address accepted")
	}
}

func TestTransportSettings(t *testing.T) {
	s := &Service{}
	tr := s.transport(target{httpLoopback: true, loopback: true})
	if tr.Proxy != nil || tr.DialContext == nil {
		t.Error("a loopback target must bypass proxies and be pinned")
	}
	tr = s.transport(target{})
	if tr.Proxy == nil {
		t.Error("an https target should honour the system proxy")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 || tr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("tls config = %+v", tr.TLSClientConfig)
	}
}

func TestNoCookiesAreKept(t *testing.T) {
	var cookies atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("cookie") != "" {
			cookies.Add(1)
		}
		http.SetCookie(w, &http.Cookie{Name: "s", Value: "1"})
		io.WriteString(w, openAIReply("pong"))
	}))
	defer srv.Close()
	s := compat(model.ProviderOpenAICompat, srv.URL, "m", compatKey)
	jar := &http.Client{}
	s.HTTP = jar
	for range 2 {
		s.TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	}
	if cookies.Load() != 0 {
		t.Fatal("a cookie was sent back")
	}
}

func TestExtractJSONIsLinear(t *testing.T) {
	half := maxExtract / 2
	for name, in := range map[string]string{
		"nested braces":       strings.Repeat("{", half) + strings.Repeat("}", half),
		"many failed objects": strings.Repeat("{x}", maxExtract/3),
		"many empty prefixes": strings.Repeat("{ ", half),
		"closers only":        strings.Repeat("}", maxExtract),
		"quotes":              strings.Repeat(`{"`, half),
		"objects then answer": strings.Repeat("{x} ", 100) + `{"answer":"a","suggestions":[]}`,
	} {
		start := time.Now()
		_, _ = extractJSON(in)
		_, _ = extractAdvice(in)
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
	// a few junk braces before the answer are fine, many are not
	few := strings.Repeat("{x} ", 5) + `{"answer":"a","suggestions":[]}`
	if got, err := extractJSON(few); err != nil || got != `{"answer":"a","suggestions":[]}` {
		t.Errorf("few: %q %v", got, err)
	}
	if _, err := extractJSON(strings.Repeat("{x} ", maxCandidates+1) + `{"answer":"a","suggestions":[]}`); err == nil {
		t.Error("candidate cap not applied")
	}
}

func TestOneDeadlineForRequestAndRetry(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(400)
		io.WriteString(w, "response_format unsupported")
	}))
	defer srv.Close()
	s := compat(model.ProviderOpenAICompat, srv.URL, "m", compatKey)
	tg, ce := s.target(model.ProviderOpenAICompat, model.AiEndpoint{BaseUrl: srv.URL, Model: "m"}, compatKey)
	if ce != nil {
		t.Fatal(ce)
	}
	start := time.Now()
	_, _, ce = s.exchange(context.Background(), tg, "s", "u", 10, 300*time.Millisecond)
	if ce == nil || ce.code != codeTimeout {
		t.Fatalf("ce = %+v", ce)
	}
	if d := time.Since(start); d > 380*time.Millisecond {
		t.Fatalf("took %v: the retry got its own deadline", d)
	}
	if n.Load() != 2 {
		t.Fatalf("%d requests, want the retry to have started", n.Load())
	}
}

func TestTLSVersionFailureIsTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
	srv.StartTLS()
	defer srv.Close()
	res, _ := compat(model.ProviderOpenAICompat, srv.URL, "m", compatKey).TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	if res.Code != codeTLS {
		t.Fatalf("result = %+v", res)
	}
	for _, msg := range []string{"remote error: tls: protocol version not supported", "tls: server selected unsupported protocol version 301", "tls: failed to verify certificate: x509: certificate signed by unknown authority"} {
		if ce := classifyTransport(context.Background(), errors.New(msg)); ce.code != codeTLS {
			t.Errorf("%q -> %s", msg, ce.code)
		}
	}
}

func TestKeyForIsUsedForCompatibleKinds(t *testing.T) {
	rec := newRecorder(t, ok200(openAIReply(goodAdvice)))
	var asked string
	s := compat(model.ProviderOpenAICompat, rec.srv.URL+"/v1", "m", compatKey)
	s.KeyFor = func(p model.AiProvider, authority string) (string, error) {
		asked = authority
		return "", errors.New("The saved API key was not saved for this address. Save the API key again in Settings.")
	}
	_, err := s.Ask(context.Background(), "q", model.AiContext{})
	if err == nil || !strings.Contains(err.Error(), "Save the API key again") || asked != strings.TrimPrefix(rec.srv.URL, "http://") {
		t.Fatalf("err %v asked %q", err, asked)
	}
	res, _ := s.TestConnection(context.Background(), model.ProviderOpenAICompat, nil)
	if res.OK || res.Code != codeNoKey || !strings.Contains(res.Message, "Save the API key again") {
		t.Fatalf("result = %+v", res)
	}
	if rec.count() != 0 {
		t.Fatal("a request was sent with an unbound key")
	}
	// a key for the right authority goes through, and only that one is sent
	s.KeyFor = func(p model.AiProvider, authority string) (string, error) { return "bound-key", nil }
	if _, err := s.Ask(context.Background(), "q", model.AiContext{}); err != nil {
		t.Fatal(err)
	}
	if rec.at(0).headers.Get("authorization") != "Bearer bound-key" {
		t.Errorf("headers = %v", rec.at(0).headers)
	}
}
