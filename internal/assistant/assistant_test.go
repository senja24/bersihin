package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/testenv"
)

const home = "/Users/agus"

func sampleContext() model.AiContext {
	return model.AiContext{
		Disk: &model.AiDisk{Total: 500 << 30, Free: 60 << 30},
		Junk: []model.AiJunk{{Name: "User caches", Size: 4 << 30, Locations: []string{"/Users/agus/Library/Caches", "~/Library/Logs"}}},
		LargeFiles: []model.AiLargeFile{
			{Path: "/Users/agus/Movies/film.mkv", Size: 9 << 30, Modified: 1741944413000},
			{Path: "/Users/AGUS/Downloads/old.dmg", Size: 2 << 30},
			{Path: "/Users/agustin/Downloads/other.dmg", Size: 1 << 30},
		},
		Spaces: []model.AiSpace{{Name: "Movies", Size: 20 << 30}, {Name: "/Users/agus", Size: 100 << 30}},
	}
}

func TestBuildContext(t *testing.T) {
	testenv.MacPaths(t)
	data, known := BuildContext(home, sampleContext())
	b, _ := json.Marshal(data)
	js := string(b)
	if strings.Contains(strings.ToLower(js), "/users/agus/") || strings.Contains(js, `"/Users/agus"`) {
		t.Fatalf("home folder leaked: %s", js)
	}
	for _, want := range []string{
		`"disk":{"total":"500 GB","free":"60 GB","usedPercent":88}`,
		`"path":"~/Movies/film.mkv"`, `"modified":"2025-03-14"`, `"path":"~/Downloads/old.dmg","size":2147483648,"modified":null`,
		`"path":"/Users/agustin/Downloads/other.dmg"`, // another account is not our home
		`"locations":["~/Library/Caches","~/Library/Logs"]`, `"name":"~"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("context lacks %s\n%s", want, js)
		}
	}
	for _, k := range []string{"user caches", "~/library/caches", "~/library/logs"} {
		if !known[k] {
			t.Errorf("%q not remembered", k)
		}
	}
	for _, k := range []string{"~/movies/film.mkv", "movies", "~"} {
		if known[k] {
			t.Errorf("%q may not be called safe", k)
		}
	}
	// largest first
	if strings.Index(js, "film.mkv") > strings.Index(js, "old.dmg") {
		t.Error("large files not sorted by size")
	}
}

func TestBuildContextCapsAndCleans(t *testing.T) {
	var c model.AiContext
	for i := range 100 {
		c.LargeFiles = append(c.LargeFiles, model.AiLargeFile{Path: "/p/" + strings.Repeat("x", 500) + string(rune('a'+i%26)), Size: int64(i)})
		c.Junk = append(c.Junk, model.AiJunk{Name: "j" + string(rune('a'+i%26)), Locations: make([]string, 10)})
		c.Spaces = append(c.Spaces, model.AiSpace{Name: "s", Size: int64(i)})
	}
	c.Junk = append(c.Junk, model.AiJunk{Name: ""})
	c.Disk = &model.AiDisk{Total: 0}
	d, _ := BuildContext(home, c)
	if len(d.Large) != 60 || len(d.Junk) != 40 || len(d.Space) != 20 || d.Disk != nil {
		t.Fatalf("large %d junk %d spaces %d disk %v", len(d.Large), len(d.Junk), len(d.Space), d.Disk)
	}
	if len(d.Large[0].Path) != 400 || len(d.Junk[0].Locations) != 6 || d.Large[0].Size != 99 {
		t.Errorf("not trimmed or sorted: %d %d %d", len(d.Large[0].Path), len(d.Junk[0].Locations), d.Large[0].Size)
	}
	// negative sizes are clamped
	d, _ = BuildContext(home, model.AiContext{Spaces: []model.AiSpace{{Name: "a", Size: -5}}, Disk: &model.AiDisk{Total: 100, Free: -1}})
	if d.Space[0].Size != 0 {
		t.Errorf("size = %d", d.Space[0].Size)
	}
}

func TestTildeUntilde(t *testing.T) {
	testenv.MacPaths(t)
	for in, want := range map[string]string{
		"/Users/agus": "~", "/Users/agus/": "~", "/Users/agus//a/../a/b/": "~/a/b", "/Users/agus/a/b": "~/a/b", "/users/AGUS/a": "~/a",
		"/Users/agustin/a": "/Users/agustin/a", "/tmp/x": "/tmp/x", "": "",
	} {
		if got := Tilde(home, in); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"~": home, "~/a/b": home + "/a/b", "~x": "~x", "/abs": "/abs"} {
		if got := Untilde(home, in); got != want {
			t.Errorf("Untilde(%q) = %q, want %q", in, got, want)
		}
	}
	if got := redact(home, "is /Users/agus/Library big? also /Users/agustin and /users/agus."); got != "is ~/Library big? also /Users/agustin and ~." {
		t.Errorf("redact = %q", got)
	}
}

func TestParseAdvice(t *testing.T) {
	testenv.MacPaths(t)
	_, known := BuildContext(home, sampleContext())
	raw := `{"answer":"  Remove the caches.  ","suggestions":[
		{"path":"~/Library/Caches","verdict":"safe","reason":"Regenerable."},
		{"path":"~/Movies/film.mkv","verdict":"safe","reason":"A video the user made."},
		{"path":"~/Documents/secret","verdict":"safe","reason":"Invented path."},
		{"path":"~/Downloads/old.dmg","verdict":"maybe","reason":"Unknown verdict."},
		{"path":"~/Pictures","verdict":"keep","reason":"Photos."},
		{"path":"","verdict":"safe","reason":"No path."},
		{"path":"/Applications/Foo.app","verdict":"REVIEW","reason":"x"}
	]}`
	adv, err := ParseAdvice(home, []byte(raw), known, model.ProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	if adv.Answer != "Remove the caches." || adv.Provider != model.ProviderClaude || len(adv.Suggestions) != 6 {
		t.Fatalf("advice = %+v", adv)
	}
	got := map[string]model.AiSuggestion{}
	for _, s := range adv.Suggestions {
		got[s.Path] = s
	}
	// "safe" stays only for junk Bersihin showed; a large file tops out at "review"
	if got[home+"/Library/Caches"].Verdict != model.VerdictSafe {
		t.Errorf("junk location lost its verdict: %+v", got)
	}
	if got[home+"/Movies/film.mkv"].Verdict != model.VerdictReview {
		t.Errorf("a large file was called safe: %+v", got[home+"/Movies/film.mkv"])
	}
	if got[home+"/Documents/secret"].Verdict != model.VerdictReview {
		t.Errorf("an invented path stayed safe: %+v", got[home+"/Documents/secret"])
	}
	if got[home+"/Downloads/old.dmg"].Verdict != model.VerdictReview || got[home+"/Pictures"].Verdict != model.VerdictKeep {
		t.Errorf("verdicts: %+v", got)
	}
	if got["/Applications/Foo.app"].Verdict != model.VerdictReview {
		t.Errorf("uppercase verdict: %+v", got["/Applications/Foo.app"])
	}
}

func TestParseAdviceRejectsBadAnswers(t *testing.T) {
	for _, raw := range []string{"", "not json", "[]", `{"suggestions":[]}`, `{"answer":"   "}`, `{"answer":5}`} {
		if _, err := ParseAdvice(home, []byte(raw), nil, model.ProviderChatGPT); err == nil || !strings.HasPrefix(err.Error(), "ChatGPT sent an answer") {
			t.Errorf("ParseAdvice(%q) err = %v", raw, err)
		}
	}
	adv, err := ParseAdvice(home, []byte(`{"answer":"ok"}`), nil, model.ProviderClaude)
	if err != nil || adv.Suggestions == nil || len(adv.Suggestions) != 0 {
		t.Fatalf("adv = %+v err = %v", adv, err)
	}
	var many strings.Builder
	many.WriteString(`{"answer":"x","suggestions":[`)
	for i := range 60 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"path":"/p/` + string(rune('a'+i%26)) + `","verdict":"keep","reason":"` + strings.Repeat("r", 400) + `"}`)
	}
	many.WriteString("]}")
	adv, _ = ParseAdvice(home, []byte(many.String()), nil, model.ProviderClaude)
	if len(adv.Suggestions) != 40 || len(adv.Suggestions[0].Reason) != 300 {
		t.Errorf("caps: %d suggestions, reason %d", len(adv.Suggestions), len(adv.Suggestions[0].Reason))
	}
}

const (
	claudeKey = "sk-ant-api03-SECRETSECRETSECRET1234567890"
	openaiKey = "sk-proj-OPENAIOPENAIOPENAI1234567890"
)

func service(url string, provider model.AiProvider, key string) *Service {
	return &Service{
		Home:         home,
		Provider:     func() model.AiProvider { return provider },
		Key:          func(model.AiProvider) (string, error) { return key, nil },
		HTTP:         &http.Client{},
		AnthropicURL: url,
		OpenAIURL:    url,
	}
}

func TestAskClaude(t *testing.T) {
	testenv.MacPaths(t)
	var gotHeaders http.Header
	var gotBody map[string]any
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		rawBody = string(b)
		json.Unmarshal(b, &gotBody)
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"stop_reason":"end_turn","content":[
			{"type":"thinking","thinking":"hmm"},
			{"type":"text","text":"{\"answer\":\"Clear the caches.\",\"suggestions\":[{\"path\":\"~/Library/Caches\",\"verdict\":\"safe\",\"reason\":\"Regenerable.\"}]}"}]}`)
	}))
	defer srv.Close()

	adv, err := service(srv.URL, model.ProviderClaude, claudeKey).Ask(context.Background(), "What can I delete from /Users/agus/Library?", sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if adv.Provider != model.ProviderClaude || adv.Answer != "Clear the caches." || len(adv.Suggestions) != 1 ||
		adv.Suggestions[0].Path != home+"/Library/Caches" || adv.Suggestions[0].Verdict != model.VerdictSafe {
		t.Fatalf("advice = %+v", adv)
	}

	if gotHeaders.Get("x-api-key") != claudeKey || gotHeaders.Get("anthropic-version") != config.AnthropicVersion ||
		gotHeaders.Get("content-type") != "application/json" || gotHeaders.Get("authorization") != "" {
		t.Errorf("headers = %v", gotHeaders)
	}
	if gotBody["model"] != config.ClaudeModel || gotBody["system"] != SystemPrompt || gotBody["max_tokens"] != float64(config.AssistantMaxTokens) {
		t.Errorf("body = %v", gotBody)
	}
	format := gotBody["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"].(map[string]any)["required"] == nil {
		t.Errorf("output_config = %v", gotBody["output_config"])
	}
	if _, has := gotBody["thinking"]; has {
		t.Error("a thinking parameter was sent")
	}
	msgs := gotBody["messages"].([]any)
	user := msgs[0].(map[string]any)["content"].(string)
	if msgs[0].(map[string]any)["role"] != "user" || len(msgs) != 1 {
		t.Errorf("messages = %v", msgs)
	}
	// privacy: names, paths and sizes with ~, never the home folder or the key
	for _, leak := range []string{"/Users/agus/", "/Users/AGUS", claudeKey, "SECRETSECRET"} {
		if strings.Contains(rawBody, leak) {
			t.Errorf("request body contains %q", leak)
		}
	}
	for _, want := range []string{"Question: What can I delete from ~/Library?", "<bersihin_data>", "~/Movies/film.mkv", "</bersihin_data>", "/Users/agustin/Downloads/other.dmg"} {
		if !strings.Contains(user, want) {
			t.Errorf("user message lacks %q:\n%s", want, user)
		}
	}
}

func TestAskOpenAI(t *testing.T) {
	testenv.MacPaths(t)
	var gotHeaders http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"answer\":\"Keep the film.\",\"suggestions\":[{\"path\":\"~/Movies/film.mkv\",\"verdict\":\"keep\",\"reason\":\"Yours.\"}]}"}}]}`)
	}))
	defer srv.Close()

	adv, err := service(srv.URL, model.ProviderChatGPT, openaiKey).Ask(context.Background(), "Is the film safe to delete?", sampleContext())
	if err != nil {
		t.Fatal(err)
	}
	if adv.Provider != model.ProviderChatGPT || adv.Answer != "Keep the film." || adv.Suggestions[0].Verdict != model.VerdictKeep {
		t.Fatalf("advice = %+v", adv)
	}
	if gotHeaders.Get("authorization") != "Bearer "+openaiKey || gotHeaders.Get("x-api-key") != "" {
		t.Errorf("headers = %v", gotHeaders)
	}
	if gotBody["model"] != config.OpenAIModel || gotBody["response_format"].(map[string]any)["type"] != "json_object" ||
		gotBody["max_completion_tokens"] != float64(config.AssistantMaxTokens) {
		t.Errorf("body = %v", gotBody)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != SystemPrompt ||
		msgs[1].(map[string]any)["role"] != "user" || strings.Contains(msgs[1].(map[string]any)["content"].(string), "/Users/agus/") {
		t.Errorf("messages = %v", msgs)
	}
}

func TestAskErrors(t *testing.T) {
	reply := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, body)
		}))
	}
	tests := []struct {
		name     string
		provider model.AiProvider
		status   int
		body     string
		want     string
	}{
		{"claude 401", model.ProviderClaude, 401, `{"error":"` + claudeKey + `"}`, "Your Claude API key was rejected. Check it in Settings."},
		{"claude 403", model.ProviderClaude, 403, ``, "Your Claude API key does not have access to this model."},
		{"claude 429", model.ProviderClaude, 429, ``, "Claude is getting too many requests right now. Wait a minute and try again."},
		{"claude 400", model.ProviderClaude, 400, ``, "Claude could not process this request. Try a shorter question."},
		{"claude 529", model.ProviderClaude, 529, ``, "Claude is having trouble right now. Please try again shortly."},
		{"claude 418", model.ProviderClaude, 418, ``, "Claude returned an error (418). Please try again."},
		{"claude refusal", model.ProviderClaude, 200, `{"stop_reason":"refusal","content":[]}`, "Claude could not help with that request. Try asking in a different way."},
		{"claude cut off", model.ProviderClaude, 200, `{"stop_reason":"max_tokens","content":[{"type":"text","text":"{"}]}`, "The answer got cut off. Try a narrower question."},
		{"claude no text", model.ProviderClaude, 200, `{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"x"}]}`, "Claude sent an answer Bersihin could not read. Please try again."},
		{"claude not json", model.ProviderClaude, 200, `<html>`, "Claude sent an answer Bersihin could not read. Please try again."},
		{"claude bad advice", model.ProviderClaude, 200, `{"content":[{"type":"text","text":"{\"nope\":1}"}]}`, "Claude sent an answer Bersihin could not read. Please try again."},
		{"openai 401", model.ProviderChatGPT, 401, ``, "Your OpenAI API key was rejected. Check it in Settings."},
		{"openai 403", model.ProviderChatGPT, 403, ``, "Your OpenAI API key does not have access to this model."},
		{"openai 429", model.ProviderChatGPT, 429, ``, "ChatGPT is busy or your OpenAI quota is used up. Try again later."},
		{"openai 500", model.ProviderChatGPT, 500, ``, "ChatGPT is having trouble right now. Please try again shortly."},
		{"openai refusal", model.ProviderChatGPT, 200, `{"choices":[{"message":{"refusal":"no"}}]}`, "ChatGPT could not help with that request. Try asking in a different way."},
		{"openai filter", model.ProviderChatGPT, 200, `{"choices":[{"finish_reason":"content_filter","message":{"content":"x"}}]}`, "ChatGPT could not help with that request. Try asking in a different way."},
		{"openai length", model.ProviderChatGPT, 200, `{"choices":[{"finish_reason":"length","message":{"content":"{"}}]}`, "The answer got cut off. Try a narrower question."},
		{"openai empty", model.ProviderChatGPT, 200, `{"choices":[]}`, "ChatGPT sent an answer Bersihin could not read. Please try again."},
		{"openai bad content", model.ProviderChatGPT, 200, `{"choices":[{"finish_reason":"stop","message":{"content":"sure!"}}]}`, "ChatGPT sent an answer Bersihin could not read. Please try again."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := reply(tc.status, tc.body)
			defer srv.Close()
			_, err := service(srv.URL, tc.provider, claudeKey).Ask(context.Background(), "q", model.AiContext{})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), claudeKey) {
				t.Fatal("error contains the key")
			}
		})
	}
}

func TestAskNetworkFailures(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := service(url, model.ProviderClaude, claudeKey).Ask(context.Background(), "q", model.AiContext{})
	if err == nil || err.Error() != "Could not reach Claude. Check your internet connection." || strings.Contains(err.Error(), claudeKey) {
		t.Fatalf("err = %v", err)
	}
	_, err = service(url, model.ProviderChatGPT, openaiKey).Ask(context.Background(), "q", model.AiContext{})
	if err == nil || err.Error() != "Could not reach ChatGPT. Check your internet connection." {
		t.Fatalf("err = %v", err)
	}

	// a server that answers too slowly
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	s := service(slow.URL, model.ProviderChatGPT, openaiKey)
	s.HTTP = &http.Client{Timeout: 50 * time.Millisecond}
	if _, err = s.Ask(context.Background(), "q", model.AiContext{}); err == nil || err.Error() != "ChatGPT took too long to answer. Please try again." {
		t.Fatalf("err = %v", err)
	}

	// the page goes away
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	s.HTTP = &http.Client{}
	if _, err = s.Ask(ctx, "q", model.AiContext{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestAskPreconditions(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	tests := []struct {
		name     string
		provider model.AiProvider
		key      string
		keyErr   error
		question string
		want     string
	}{
		{"empty question", model.ProviderClaude, claudeKey, nil, "  \n ", "Type a question for the assistant first."},
		{"long question", model.ProviderClaude, claudeKey, nil, strings.Repeat("a", 2001), "Please keep your question under 2000 characters."},
		{"no claude key", model.ProviderClaude, "", nil, "hi", "Add your Claude API key in Settings to use the assistant."},
		{"no openai key", model.ProviderChatGPT, "", nil, "hi", "Add your ChatGPT (OpenAI) API key in Settings to use the assistant."},
		{"keychain error", model.ProviderClaude, "", errors.New("The saved API key could not be read"), "hi", "The saved API key could not be read"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := service(srv.URL, tc.provider, tc.key)
			s.Key = func(model.AiProvider) (string, error) { return tc.key, tc.keyErr }
			_, err := s.Ask(context.Background(), tc.question, model.AiContext{})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if called {
		t.Fatal("a request was sent although a precondition failed")
	}
	if !slices.Equal([]string{displayName(model.ProviderClaude), displayName(model.ProviderChatGPT)}, []string{"Claude", "ChatGPT"}) {
		t.Fatal("names")
	}
}

func TestSystemPromptMentionsTheRules(t *testing.T) {
	for _, want := range []string{"Bersihin", "macOS", "review", "Trash", "never see file contents", "JSON"} {
		if !strings.Contains(strings.ToLower(SystemPrompt), strings.ToLower(want)) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	for _, bad := range []string{"Windows", "Cleanly", "Recycle Bin"} {
		if strings.Contains(SystemPrompt, bad) {
			t.Errorf("system prompt still mentions %q", bad)
		}
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hit bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		io.WriteString(w, `{"content":[{"type":"text","text":"{\"answer\":\"x\"}"}]}`)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	for _, p := range []model.AiProvider{model.ProviderClaude, model.ProviderChatGPT} {
		s := service(srv.URL, p, claudeKey)
		s.HTTP = &http.Client{} // a client that would follow
		_, err := s.Ask(context.Background(), "q", model.AiContext{})
		if err == nil || !strings.Contains(err.Error(), "(307)") {
			t.Errorf("%s: err = %v", p, err)
		}
	}
	if hit {
		t.Fatal("the redirect target received a request (and the API key)")
	}
}

func TestRedactsJunkNamesAndUncleanedPaths(t *testing.T) {
	testenv.MacPaths(t)
	data, _ := BuildContext(home, model.AiContext{
		Junk:       []model.AiJunk{{Name: "Cache of /Users/agus/Library", Locations: []string{"/Users/agus//Library/../Library/Caches/"}}},
		LargeFiles: []model.AiLargeFile{{Path: "/Users/agus/Movies/./x.mkv", Size: 1}},
	})
	b, _ := json.Marshal(data)
	if strings.Contains(string(b), "agus") && !strings.Contains(string(b), "~") || strings.Contains(string(b), "/Users/agus") {
		t.Fatalf("leak: %s", b)
	}
	if data.Junk[0].Locations[0] != "~/Library/Caches" || data.Large[0].Path != "~/Movies/x.mkv" {
		t.Fatalf("data = %+v", data)
	}
}
