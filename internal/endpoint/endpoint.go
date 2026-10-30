// Package endpoint checks the address of a user-entered AI endpoint and builds
// the request URL from it. It is a leaf package: settings use it when the
// address is saved and loaded, the assistant uses it before every call, so
// the address is never trusted just because it was stored.
//
// An address is accepted when it is https, or http to this machine only
// (localhost, *.localhost, 127.0.0.0/8, ::1). Link-local and unspecified
// addresses (the cloud metadata service 169.254.169.254, 0.0.0.0, ::) are
// refused even over https, and so are addresses with credentials, a query or a
// fragment.
package endpoint

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// Limits of an address and of a model name.
const (
	MaxURL   = 2048
	MaxModel = 128
)

// Kind is the wire format of an endpoint.
type Kind string

// The two wire formats.
const (
	OpenAI Kind = "openai" // POST {base}/chat/completions
	Claude Kind = "claude" // POST {base}/messages
)

// Endpoint is a checked address.
type Endpoint struct {
	// Base is the normalised address: scheme, host, optional port and path,
	// no trailing slash. It is what settings store and Join extends.
	Base   string
	Scheme string
	// Host is the ASCII, lower case host name or IP, without brackets.
	Host string
	// Port is the explicit port, or the scheme's default.
	Port string
	// Path is the escaped path without a trailing slash ("" for none).
	Path string
	// Loopback is true for localhost, *.localhost and loopback IPs.
	Loopback bool
}

// Authority is host:port. A stored API key may only travel to the authority
// it was saved with.
func (e Endpoint) Authority() string { return net.JoinHostPort(e.Host, e.Port) }

// SameOrigin reports whether o has the same scheme, host and port.
func (e Endpoint) SameOrigin(o Endpoint) bool {
	return e.Scheme == o.Scheme && e.Host == o.Host && e.Port == o.Port
}

// Binding is the full address a stored API key is tied to: scheme, host, the
// effective port and the path, so spellings of one address (default port
// written or not, trailing slash, letter case of the host) give the same
// value. Shared gateways separate tenants by path, hence the path is in it.
func (e Endpoint) Binding() string { return e.Scheme + "://" + e.Authority() + e.Path }

// HostPath is the host and path without scheme, for messages: it never holds
// credentials, a query or a key.
func (e Endpoint) HostPath() string {
	i := strings.Index(e.Base, "://")
	return e.Base[i+3:]
}

// Display is the host, with the port when it is not the scheme's default,
// for messages: it never holds credentials, a path, a query or a key.
func (e Endpoint) Display() string {
	h := e.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	if (e.Scheme == "https" && e.Port == "443") || (e.Scheme == "http" && e.Port == "80") {
		return h
	}
	return h + ":" + e.Port
}

// Parse checks raw and returns it normalised. The messages are meant for the
// user.
func Parse(raw string) (Endpoint, error) {
	if raw == "" {
		return Endpoint{}, errors.New("Enter the address of the server.")
	}
	if len(raw) > MaxURL {
		return Endpoint{}, errors.New("That address is too long.")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' {
			return Endpoint{}, errors.New("The address may not contain spaces, control characters or backslashes.")
		}
	}
	if strings.ContainsAny(raw, "?#") {
		return Endpoint{}, errors.New("The address may not contain a query (?) or a fragment (#).")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return Endpoint{}, errors.New("That does not look like a web address.")
	}
	if u.User != nil || strings.Contains(u.Host, "@") {
		return Endpoint{}, errors.New("The address may not contain a user name or password. Put the key in the API key field.")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return Endpoint{}, errors.New("The address must start with https://.")
	}
	if strings.HasSuffix(u.Host, ":") {
		return Endpoint{}, errors.New("The port in the address is not valid.")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Endpoint{}, errors.New("The port in the address is not valid.")
		}
		port = strconv.Itoa(n)
	}
	host, loopback, err := checkHost(u.Hostname())
	if err != nil {
		return Endpoint{}, err
	}
	if u.Scheme == "http" && !loopback {
		return Endpoint{}, errors.New("http:// is only allowed for this computer (localhost). Use https:// for any other server.")
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return Endpoint{}, errors.New("The address path may not contain . or .. parts.")
		}
	}

	e := Endpoint{Scheme: u.Scheme, Host: host, Port: port, Loopback: loopback, Path: strings.TrimRight(u.EscapedPath(), "/")}
	if e.Port == "" {
		e.Port = "443"
		if u.Scheme == "http" {
			e.Port = "80"
		}
	}
	hostPort := host
	if strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}
	if port != "" {
		hostPort += ":" + port
	}
	e.Base = u.Scheme + "://" + hostPort + e.Path
	return e, nil
}

// checkHost validates a host name or IP and says whether it is this machine.
func checkHost(h string) (host string, loopback bool, err error) {
	bad := errors.New("The server name in the address is not valid.")
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || strings.Contains(h, "%") {
		return "", false, bad
	}
	if ip, perr := netip.ParseAddr(h); perr == nil {
		ip = ip.Unmap()
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return "", false, errors.New("That address is not allowed (unspecified, link-local, multicast or cloud metadata).")
		}
		return ip.String(), ip.IsLoopback(), nil
	}
	if !isASCII(h) {
		a, err := idna.Lookup.ToASCII(h)
		if err != nil {
			return "", false, bad
		}
		h = strings.ToLower(a)
	}
	if len(h) > 253 {
		return "", false, bad
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", false, bad
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return "", false, bad
			}
		}
	}
	// A name that ends in a number is an IP in a shorthand the system resolver
	// may still accept (127.1, 2852039166, 0x7f.1); none of them is a real name.
	last := labels[len(labels)-1]
	if last[0] >= '0' && last[0] <= '9' {
		return "", false, errors.New("Write an IP address in full (for example 127.0.0.1) or use a server name.")
	}
	return h, h == "localhost" || strings.HasSuffix(h, ".localhost"), nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Join returns the URL to POST to. It appends /chat/completions (OpenAI) or
// /messages (Claude) to base unless base already ends with it; a base without
// a path gets /v1 first.
func Join(base string, k Kind) string {
	base = strings.TrimRight(base, "/")
	suffix := "/chat/completions"
	if k == Claude {
		suffix = "/messages"
	}
	if strings.HasSuffix(base, suffix) {
		return base
	}
	rest := base
	if i := strings.Index(base, "://"); i >= 0 {
		rest = base[i+3:]
	}
	if !strings.Contains(rest, "/") {
		return base + "/v1" + suffix
	}
	return base + suffix
}

// CheckModel validates a model name: 1..128 characters, no control, format
// (bidi, zero-width), line or paragraph separator characters, no space other
// than U+0020 (no U+00A0 and the like) and no Hangul filler (U+115F, U+1160,
// U+3164, U+FFA0), which render as nothing.
func CheckModel(name string) error {
	if name == "" || len([]rune(name)) > MaxModel {
		return errors.New("The model name must be 1 to 128 characters long.")
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) ||
			(unicode.Is(unicode.Zs, r) && r != ' ') || r == 0x115F || r == 0x1160 || r == 0x3164 || r == 0xFFA0 {
			return errors.New("The model name may not contain control or invisible characters, line separators or special spaces.")
		}
	}
	return nil
}
