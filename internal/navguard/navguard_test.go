package navguard

import "testing"

const dev = "http://localhost:5173"

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		url  string
		dev  string
		user bool
		want Action
	}{
		{"app origin", "mygo://localhost/", "", false, Allow},
		{"app origin path", "mygo://localhost/index.html#/cleanup", "", false, Allow},
		{"app origin upper", "MYGO://LOCALHOST/x", "", false, Allow},
		{"other mygo host", "mygo://evil.example/", "", false, Deny},
		{"mygo userinfo trick", "mygo://localhost@evil.example/", "", false, Deny},
		{"dev server in dev", "http://localhost:5173/", dev, false, Allow},
		{"dev server in build", "http://localhost:5173/", "", false, Deny},
		{"other localhost port in dev", "http://localhost:8080/", dev, false, Deny},
		{"other loopback in dev", "http://127.0.0.1:5173/", dev, false, Deny},
		{"file", "file:///etc/passwd", "", true, Deny},
		{"file in dev", "file:///etc/passwd", dev, true, Deny},
		{"about", "about:blank", "", false, Deny},
		{"javascript", "javascript:alert(1)", "", true, Deny},
		{"data", "data:text/html,<b>x</b>", "", true, Deny},
		{"custom scheme", "x-apple.systempreferences:foo", "", true, Deny},
		{"https click", "https://example.com/docs", "", true, OpenExternally},
		{"https script", "https://example.com/docs", "", false, Deny},
		{"http click", "http://example.com/", "", true, Deny},
		{"https no host", "https://", "", true, Deny},
		{"garbage", "::::", "", true, Deny},
		{"empty", "", "", true, Deny},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.url, c.dev, c.user); got != c.want {
				t.Errorf("Decide(%q, %q, %v) = %v, want %v", c.url, c.dev, c.user, got, c.want)
			}
		})
	}
}

func TestDevOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:5173":   "http://localhost:5173",
		"http://localhost:5173/x": "http://localhost:5173",
		"":                        "",
		"::::":                    "",
		"file:///etc":             "",
	} {
		if got := DevOrigin(in); got != want {
			t.Errorf("DevOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenFromNewWindow(t *testing.T) {
	for _, c := range []struct {
		url  string
		user bool
		want bool
	}{
		{"https://example.com/", true, true},
		{"https://example.com/", false, false}, // pop-up or exfiltration by script
		{"http://example.com/", true, false},
		{"file:///etc/passwd", true, false},
		{"mygo://localhost/", true, false}, // never a second window
		{"javascript:alert(1)", true, false},
		{"https://example.com/?d=secret", false, false},
	} {
		if got := OpenFromNewWindow(c.url, c.user); got != c.want {
			t.Errorf("OpenFromNewWindow(%q, %v) = %v, want %v", c.url, c.user, got, c.want)
		}
	}
}
