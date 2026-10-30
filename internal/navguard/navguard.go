// Package navguard decides which navigations the app's window may make. MyGo
// trusts file:, about:, custom schemes and dev localhost pages with the bridge
// to Go, so the window must never leave its own pages.
package navguard

import (
	"net/url"
	"strings"
)

// Action is what to do with a navigation.
type Action int

const (
	// Deny cancels the navigation.
	Deny Action = iota
	// Allow lets the page go there: it is the app's own origin.
	Allow
	// OpenExternally cancels the navigation and opens the URL in the browser.
	OpenExternally
)

func (a Action) String() string {
	switch a {
	case Allow:
		return "allow"
	case OpenExternally:
		return "open externally"
	}
	return "deny"
}

// appHost is the host of the embedded frontend, mygo://localhost.
const appHost = "localhost"

// DevOrigin returns "scheme://host" of the dev server URL, or "" when raw is
// not an http(s) URL. Pass the origin only while serving (mygo dev).
func DevOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// Decide returns what to do with a navigation to rawURL. devOrigin is the dev
// server's origin during `mygo dev` and "" in builds. Only the app's own
// origin is allowed; an https link a user clicked opens in the browser;
// everything else is denied.
func Decide(rawURL, devOrigin string, userInitiated bool) Action {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Deny
	}
	scheme := strings.ToLower(u.Scheme)
	if u.User == nil {
		if scheme == "mygo" && strings.EqualFold(u.Host, appHost) {
			return Allow
		}
		if devOrigin != "" && (scheme == "http" || scheme == "https") && scheme+"://"+strings.ToLower(u.Host) == devOrigin {
			return Allow
		}
	}
	if scheme == "https" && u.Host != "" && u.User == nil && userInitiated {
		return OpenExternally
	}
	return Deny
}

// OpenFromNewWindow reports whether a window.open() or target=_blank request
// may be opened in the browser. The app never opens a second window, and a
// script that was not started by a user gesture must not reach the network.
func OpenFromNewWindow(rawURL string, userInitiated bool) bool {
	return Decide(rawURL, "", userInitiated) == OpenExternally
}
