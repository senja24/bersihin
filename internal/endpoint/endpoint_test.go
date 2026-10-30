package endpoint

import (
	"strings"
	"testing"
)

func TestParseAccepts(t *testing.T) {
	tests := []struct {
		in, base, host, port string
		loopback             bool
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1", "api.openai.com", "443", false},
		{"HTTPS://API.Example.COM/V1/", "https://api.example.com/V1", "api.example.com", "443", false},
		{"https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1", "openrouter.ai", "443", false},
		{"https://example.com", "https://example.com", "example.com", "443", false},
		{"https://example.com/", "https://example.com", "example.com", "443", false},
		{"https://example.com:8443/openai/v1///", "https://example.com:8443/openai/v1", "example.com", "8443", false},
		{"https://my_host.internal/v4", "https://my_host.internal/v4", "my_host.internal", "443", false},
		{"https://example.com./v1", "https://example.com/v1", "example.com", "443", false},
		{"https://bücher.example/v1", "https://xn--bcher-kva.example/v1", "xn--bcher-kva.example", "443", false},
		{"http://localhost:11434/v1", "http://localhost:11434/v1", "localhost", "11434", true},
		{"http://localhost", "http://localhost", "localhost", "80", true},
		{"http://LOCALHOST:1234", "http://localhost:1234", "localhost", "1234", true},
		{"http://ollama.localhost:11434", "http://ollama.localhost:11434", "ollama.localhost", "11434", true},
		{"http://127.0.0.1:1234/v1", "http://127.0.0.1:1234/v1", "127.0.0.1", "1234", true},
		{"http://127.8.9.10/v1", "http://127.8.9.10/v1", "127.8.9.10", "80", true},
		{"http://[::1]:8080/v1", "http://[::1]:8080/v1", "::1", "8080", true},
		{"http://[::ffff:127.0.0.1]:8080", "http://127.0.0.1:8080", "127.0.0.1", "8080", true},
		{"https://localhost:8443/v1", "https://localhost:8443/v1", "localhost", "8443", true},
		{"https://192.168.1.5/v1", "https://192.168.1.5/v1", "192.168.1.5", "443", false},
		{"https://[2001:db8::1]/v1", "https://[2001:db8::1]/v1", "2001:db8::1", "443", false},
		{"https://example.com/v1/chat/completions", "https://example.com/v1/chat/completions", "example.com", "443", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			e, err := Parse(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if e.Base != tc.base || e.Host != tc.host || e.Port != tc.port || e.Loopback != tc.loopback {
				t.Fatalf("got %+v", e)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct{ name, in string }{
		{"empty", ""},
		{"too long", "https://example.com/" + strings.Repeat("a", 2048)},
		{"no scheme", "example.com/v1"},
		{"ftp", "ftp://example.com"},
		{"file", "file:///etc/passwd"},
		{"javascript", "javascript:alert(1)"},
		{"opaque", "https:example.com"},
		{"lan http", "http://192.168.1.5"},
		{"lan http port", "http://192.168.1.5:11434/v1"},
		{"private 10 http", "http://10.0.0.2"},
		{"remote http", "http://example.com"},
		{"lookalike localhost", "http://localhost.evil.com"},
		{"lookalike localhost prefix", "http://evillocalhost"},
		{"lookalike 127", "http://127.0.0.1.evil.com"},
		{"lookalike userinfo", "http://localhost@evil.com"},
		{"userinfo", "https://user:pass@example.com/v1"},
		{"user only", "https://user@example.com"},
		{"metadata", "https://169.254.169.254/latest"},
		{"metadata http", "http://169.254.169.254"},
		{"metadata mapped", "https://[::ffff:169.254.169.254]"},
		{"link local v6", "https://[fe80::1]/v1"},
		{"link local v6 zone", "https://[fe80::1%25en0]/v1"},
		{"unspecified v4", "https://0.0.0.0/v1"},
		{"unspecified v4 http", "http://0.0.0.0:8080"},
		{"unspecified v6", "http://[::]:8080"},
		{"multicast", "https://224.0.0.1"},
		{"query", "https://example.com/v1?key=abc"},
		{"empty query", "https://example.com/v1?"},
		{"fragment", "https://example.com/v1#x"},
		{"empty host", "https:///v1"},
		{"empty host port", "https://:443/v1"},
		{"bad port", "https://example.com:abc"},
		{"port zero", "https://example.com:0"},
		{"port big", "https://example.com:65536"},
		{"empty port", "https://example.com:/v1"},
		{"space", "https://exa mple.com"},
		{"trailing space", "https://example.com "},
		{"newline", "https://example.com/v1\n"},
		{"tab", "https://example.com/\tv1"},
		{"control", "https://example.com/\x00"},
		{"backslash", "https://example.com\\@evil.com"},
		{"backslash path", "https://example.com/v1\\x"},
		{"short ipv4", "https://127.1"},
		{"decimal ip", "https://2852039166/v1"},
		{"hex ip", "https://0x7f.0.0.1"},
		{"octal ip", "https://0177.0.0.1"},
		{"leading zero ip", "https://127.000.000.001"},
		{"dot segment", "https://example.com/v1/../admin"},
		{"underscore start label dash", "https://-bad.example"},
		{"empty label", "https://a..b"},
		{"bad idn", "https://xn--.example"},
		{"bad char", "https://exa!mple.com"},
		{"long label", "https://" + strings.Repeat("a", 64) + ".com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if e, err := Parse(tc.in); err == nil {
				t.Fatalf("accepted %q as %+v", tc.in, e)
			}
		})
	}
}

func TestJoin(t *testing.T) {
	tests := []struct {
		base string
		kind Kind
		want string
	}{
		{"https://api.openai.com", OpenAI, "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/", OpenAI, "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", OpenAI, "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", OpenAI, "https://api.openai.com/v1/chat/completions"},
		{"https://openrouter.ai/api/v1", OpenAI, "https://openrouter.ai/api/v1/chat/completions"},
		{"https://x.example/openai/v1", OpenAI, "https://x.example/openai/v1/chat/completions"},
		{"https://x.example/v4", OpenAI, "https://x.example/v4/chat/completions"},
		{"https://x.example/v1/chat/completions", OpenAI, "https://x.example/v1/chat/completions"},
		{"https://x.example/v1/chat/completions/", OpenAI, "https://x.example/v1/chat/completions"},
		{"http://localhost:11434", OpenAI, "http://localhost:11434/v1/chat/completions"},
		{"http://[::1]:11434", OpenAI, "http://[::1]:11434/v1/chat/completions"},
		{"https://api.anthropic.com", Claude, "https://api.anthropic.com/v1/messages"},
		{"https://gw.example/anthropic", Claude, "https://gw.example/anthropic/messages"},
		{"https://gw.example/anthropic/v1", Claude, "https://gw.example/anthropic/v1/messages"},
		{"https://gw.example/api/v1", Claude, "https://gw.example/api/v1/messages"},
		{"https://gw.example/v1/messages", Claude, "https://gw.example/v1/messages"},
		{"https://gw.example/v1/messages//", Claude, "https://gw.example/v1/messages"},
		{"https://gw.example/v1/chat/completions", Claude, "https://gw.example/v1/chat/completions/messages"},
	}
	for _, tc := range tests {
		if got := Join(tc.base, tc.kind); got != tc.want {
			t.Errorf("Join(%q, %s) = %q, want %q", tc.base, tc.kind, got, tc.want)
		}
	}
}

func TestSameOriginAndBinding(t *testing.T) {
	a, _ := Parse("https://api.example.com/v1")
	for in, want := range map[string]bool{
		"https://API.example.com/other": true,
		"https://api.example.com:443":   true,
		"https://api.example.com:8443":  false,
		"https://api.example.org/v1":    false,
		"https://evil.api.example.com":  false,
		"http://localhost":              false,
	} {
		b, err := Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if a.SameOrigin(b) != want {
			t.Errorf("SameOrigin(%s) = %v, want %v", in, !want, want)
		}
	}
	if got := a.HostPath(); got != "api.example.com/v1" {
		t.Errorf("HostPath = %q", got)
	}
	// the binding holds scheme, host, effective port and path
	for in, want := range map[string]string{
		"https://API.example.com/v1/":          "https://api.example.com:443/v1",
		"https://api.example.com:443/v1":       "https://api.example.com:443/v1",
		"https://gw.example/v1/acct/gw/openai": "https://gw.example:443/v1/acct/gw/openai",
		"https://gw.example":                   "https://gw.example:443",
		"http://localhost:443":                 "http://localhost:443",
		"https://localhost":                    "https://localhost:443",
		"http://[::1]:8080/v1":                 "http://[::1]:8080/v1",
		"http://localhost":                     "http://localhost:80",
	} {
		e, err := Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.Binding(); got != want {
			t.Errorf("Binding(%s) = %q, want %q", in, got, want)
		}
	}
	x, _ := Parse("http://localhost:443")
	y, _ := Parse("https://localhost")
	if x.Binding() == y.Binding() || x.SameOrigin(y) {
		t.Error("a scheme change must change the binding")
	}
	p1, _ := Parse("https://gw.example/v1/a/one")
	p2, _ := Parse("https://gw.example/v1/a/two")
	if p1.Binding() == p2.Binding() || !p1.SameOrigin(p2) {
		t.Error("a path change must change the binding but not the origin")
	}
	for in, want := range map[string]string{
		"https://api.example.com/v1": "api.example.com", "https://api.example.com:8443": "api.example.com:8443",
		"http://localhost:11434/v1": "localhost:11434", "https://api.example.com:443": "api.example.com", "http://[::1]:80": "[::1]",
	} {
		e, _ := Parse(in)
		if got := e.Display(); got != want {
			t.Errorf("Display(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckModel(t *testing.T) {
	for in, ok := range map[string]bool{
		"gpt-4o": true, "meta-llama/Llama-3.3-70B-Instruct:free": true, "a": true, strings.Repeat("a", 128): true,
		"": false, strings.Repeat("a", 129): false, "bad\nname": false, "bad\x00": false, "evil\u202ename": false, "zero\u200bwidth": false, "bom\ufeffname": false, "plain name": true,
		"line\u2028sep": false, "para\u2029sep": false, "nb\u00a0sp": false, "em\u2003space": false, "ideo\u3000space": false, "narrow\u202fnb": false,
		"hangul\u3164filler": false, "hangul\u115ffiller": false, "hangul\u1160filler": false, "half\uffa0filler": false,
		"tab\there": false, "nel\u0085x": false, "soft\u00adhyphen": false,
	} {
		if (CheckModel(in) == nil) != ok {
			t.Errorf("CheckModel(%q) ok=%v, want %v", in, !ok, ok)
		}
	}
}
