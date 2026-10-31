package assistant

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"syscall"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

func displayName(p model.AiProvider) string {
	switch p {
	case model.ProviderChatGPT:
		return "ChatGPT"
	case model.ProviderClaudeCompat:
		return "The Claude-compatible server"
	case model.ProviderOpenAICompat:
		return "The OpenAI-compatible server"
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

// statusError maps an HTTP status of an official provider to a message the
// user can act on.
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

// Result codes of a failed call. They are also the codes of AiTestResult.
const (
	codeOK       = "ok"
	codeBadURL   = "bad_url"
	codeNoKey    = "no_key"
	codeNoModel  = "no_model"
	codeDNS      = "dns"
	codeRefused  = "refused"
	codeTLS      = "tls"
	codeTimeout  = "timeout"
	codeRedirect = "redirect"
	codeAuth     = "auth"
	codeNotFound = "not_found"
	codeModel    = "model"
	codeRate     = "rate"
	codeServer   = "server"
	codeTooBig   = "too_big"
	codeNotJSON  = "not_json"
	codeOther    = "other"
)

// codeMessages are the fixed texts for the codes. They never hold anything
// the server said.
var codeMessages = map[string]string{
	codeOK:       "Connected. The server answered.",
	codeBadURL:   "The address is not valid.",
	codeNoKey:    "Add the API key for this server in Settings.",
	codeNoModel:  "Add the model name in Settings.",
	codeDNS:      "The server name could not be found. Check the address.",
	codeRefused:  "The server refused the connection. Check that it is running and that the port is right.",
	codeTLS:      "The secure connection failed: the certificate is not trusted or does not match the address. Bersihin never skips this check.",
	codeTimeout:  "The server took too long to answer.",
	codeRedirect: "The server redirected the request. Enter the final address instead; Bersihin never follows redirects.",
	codeAuth:     "The server rejected the API key. Check the key.",
	codeNotFound: "Wrong address or path. Try adding or removing /v1.",
	codeModel:    "The server does not know this model. Check the model name.",
	codeRate:     "The server is busy or your quota is used up. Try again later.",
	codeServer:   "The server had an error. Try again later.",
	codeTooBig:   "The server sent an answer that is too large.",
	codeNotJSON:  "The server answered, but not in a format Bersihin understands. Check that the address matches the provider type.",
	codeOther:    "The server returned an unexpected answer. Check the address, model and key.",
}

// callError is a failed call, already classified. It carries no text from
// net/http or from the server: the page only ever sees codeMessages.
type callError struct {
	code   string
	msg    string // overrides the fixed message (static text only)
	status int
	// err is set only when the page went away; it is passed on as it is.
	err error
	// retryPlain: the server did not like the structured output option.
	retryPlain bool
	detail     string
}

func (e *callError) Error() string { return e.code }

func (e *callError) message() string {
	if e.msg != "" {
		return e.msg
	}
	return codeMessages[e.code]
}

var errNotLoopback = errors.New("connection target is not this machine")

var (
	structuredFields = []string{"response_format", "output_config", "json_schema", "json_object", "structured"}
	unknownModelRe   = regexp.MustCompile(`model_not_found|not_found_error.{0,100}model|model[^a-z]{0,4}[^"\n]{0,80}?(not found|not_found|does not exist|not exist)|(unknown|invalid|no such|unsupported) model`)
)

// classifyStatus classifies a non-2xx answer. head is the first part of the
// body; it is only looked at, never kept or shown.
func classifyStatus(status int, head []byte) *callError {
	b := strings.ToLower(string(head))
	ce := &callError{status: status, code: codeOther}
	mentionsField := false
	for _, f := range structuredFields {
		if strings.Contains(b, f) {
			mentionsField = true
		}
	}
	switch {
	case status >= 300 && status < 400:
		ce.code = codeRedirect
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		ce.code = codeAuth
	case status == http.StatusTooManyRequests:
		ce.code = codeRate
	case status >= 500:
		ce.code = codeServer
	case (status == 400 || status == 404 || status == 422) && !mentionsField && unknownModelRe.MatchString(b):
		ce.code = codeModel
	case status == http.StatusNotFound:
		ce.code = codeNotFound
		ce.retryPlain = mentionsField
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		ce.retryPlain = true
	}
	return ce
}

// classifyTransport classifies a failed round trip.
func classifyTransport(ctx context.Context, err error) *callError {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &callError{err: ctx.Err()} // the page went away
	}
	var (
		dnsErr  *net.DNSError
		certErr *tls.CertificateVerificationError
		recHdr  tls.RecordHeaderError
		unkAuth x509.UnknownAuthorityError
		hostErr x509.HostnameError
		invalid x509.CertificateInvalidError
		ne      net.Error
	)
	switch {
	case errors.Is(err, errNotLoopback):
		return &callError{code: codeBadURL, msg: "The address does not lead to this computer, so http:// is not allowed for it."}
	case errors.As(err, &dnsErr):
		return &callError{code: codeDNS}
	case errors.As(err, &certErr), errors.As(err, &recHdr), errors.As(err, &unkAuth), errors.As(err, &hostErr), errors.As(err, &invalid),
		strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return &callError{code: codeTLS}
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, errConnRefused):
		return &callError{code: codeRefused}
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return &callError{code: codeTimeout}
	case strings.Contains(err.Error(), "tls: ") || strings.Contains(err.Error(), "x509: "):
		// handshake failures such as an unsupported protocol version or a
		// failed alert; only the code leaves this function, not the text
		return &callError{code: codeTLS}
	}
	return &callError{code: codeOther}
}

type lookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// loopbackDial returns a DialContext that connects only to loopback
// addresses. The name is resolved here and every answer must be loopback, so
// an /etc/hosts entry or a DNS answer cannot send a plain-http request (and
// the key in it) to another machine; then the checked IP is dialled.
func loopbackDial(lookup lookupFunc) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var ips []netip.Addr
		if ip, perr := netip.ParseAddr(host); perr == nil {
			ips = []netip.Addr{ip}
		} else if ips, err = lookup(ctx, host); err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errNotLoopback
		}
		for _, ip := range ips {
			if !ip.Unmap().IsLoopback() {
				return nil, errNotLoopback
			}
		}
		d := net.Dialer{Timeout: 30 * time.Second}
		var last error
		for _, ip := range ips {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
}

// transport builds the transport of one call: TLS is always verified (1.2 or
// newer, no way to turn it off), proxies are not used for this machine, and a
// plain-http target is pinned to loopback.
func (s *Service) transport(t target) *http.Transport {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 20 * time.Second,
		ForceAttemptHTTP2:   true,
		DisableKeepAlives:   true,
	}
	if t.loopback || t.httpLoopback {
		tr.Proxy = nil
	}
	if t.httpLoopback {
		lookup := s.Lookup
		if lookup == nil {
			lookup = defaultLookup
		}
		tr.DialContext = loopbackDial(lookup)
	} else {
		tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	return tr
}

// post sends a JSON body and returns the response body of a 2xx answer. The
// key travels only in the headers and never appears in an error. Redirects
// are never followed, the answer is capped, and an error body is only
// classified, never kept.
func (s *Service) post(ctx context.Context, t target, body any) ([]byte, *callError) {
	fail := func(ce *callError) ([]byte, *callError) {
		if ce.err == nil && ce.detail == "" {
			ce.detail = t.hostPath
			if ce.status != 0 {
				ce.detail = fmt.Sprintf("HTTP %d at %s", ce.status, t.hostPath)
			}
		}
		return nil, ce
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fail(&callError{code: codeOther})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return fail(&callError{code: codeBadURL})
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "Bersihin/"+s.version())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	// A copy that never follows a redirect (the key must only go to the
	// endpoint it was meant for) and never keeps cookies.
	var client http.Client // the deadline is the context's, for the whole exchange
	if s.HTTP != nil {
		client = *s.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	if client.Transport == nil {
		tr := s.transport(t)
		defer tr.CloseIdleConnections()
		client.Transport = tr
	}
	resp, err := client.Do(req)
	if err != nil {
		return fail(classifyTransport(ctx, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, config.AssistantMaxErrorBody))
		return fail(classifyStatus(resp.StatusCode, head))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, config.AssistantMaxResponse+1))
	if err != nil {
		return fail(classifyTransport(ctx, err))
	}
	if len(b) > config.AssistantMaxResponse {
		return fail(&callError{code: codeTooBig})
	}
	return b, nil
}

func (s *Service) version() string {
	if s.Version != nil {
		if v := s.Version(); v != "" {
			return v
		}
	}
	return "dev"
}

// replyKind says how far an answer can be used.
type replyKind int

const (
	replyOK replyKind = iota
	replyUnreadable
	replyRefused
	replyCutOff
)

// reply is the decoded envelope of an answer: the text of the model, and
// whether the provider flagged it.
type reply struct {
	kind replyKind
	text string
}

// decodeReply reads the answer envelope in the wire format of t.
func (t target) decodeReply(raw []byte) reply {
	if t.claude() {
		return decodeClaude(raw)
	}
	return decodeOpenAI(raw)
}

func decodeClaude(raw []byte) reply {
	var resp struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return reply{kind: replyUnreadable}
	}
	var text strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	r := reply{text: text.String()}
	switch {
	case resp.StopReason == "refusal":
		r.kind = replyRefused
	case resp.StopReason == "max_tokens":
		r.kind = replyCutOff
	case text.Len() == 0:
		r.kind = replyUnreadable
	}
	return r
}

func decodeOpenAI(raw []byte) reply {
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
		return reply{kind: replyUnreadable}
	}
	c := resp.Choices[0]
	r := reply{text: c.Message.Content}
	switch {
	case c.Message.Refusal != "", c.FinishReason == "content_filter":
		r.kind = replyRefused
	case c.FinishReason == "length":
		r.kind = replyCutOff
	case strings.TrimSpace(c.Message.Content) == "":
		r.kind = replyUnreadable
	}
	return r
}

// exchange sends one request through the structured-output ladder and
// decodes the envelope. Official providers always send one structured
// request. A compatible provider first tries structured output; if the server
// answers 400 or 422 (or 404 naming the option) it retries once without it,
// asking in the system prompt instead. 401, 403, 429, 5xx, redirects and
// timeouts are never retried. The mode that worked is remembered in memory
// for that provider, address and model.
func (s *Service) exchange(ctx context.Context, t target, system, user string, maxTokens int, timeout time.Duration) (reply, mode, *callError) {
	// One deadline for the request and its retry together.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	m := modeStructured
	if t.compat() {
		if known, ok := s.knownMode(t); ok {
			m = known
		}
	}
	once := func(m mode) (reply, *callError) {
		raw, ce := s.post(ctx, t, t.body(system, user, maxTokens, m))
		if ce != nil {
			return reply{}, ce
		}
		return t.decodeReply(raw), nil
	}
	r, ce := once(m)
	if ce != nil && ce.retryPlain && t.compat() && m == modeStructured {
		m = modePlain
		r, ce = once(m)
	}
	if ce == nil && t.compat() {
		s.rememberMode(t, m)
	}
	return r, m, ce
}

func modeKey(t target) string { return string(t.provider) + "|" + t.url + "|" + t.model }

func (s *Service) knownMode(t target) (mode, bool) {
	s.modeMu.Lock()
	defer s.modeMu.Unlock()
	m, ok := s.modes[modeKey(t)]
	return m, ok
}

func (s *Service) rememberMode(t target, m mode) {
	s.modeMu.Lock()
	defer s.modeMu.Unlock()
	if s.modes == nil {
		s.modes = map[string]mode{}
	}
	s.modes[modeKey(t)] = m
}

// askError turns a failed call into the error the page shows. Official
// providers keep their own wording; compatible ones use the fixed texts.
func askError(p model.AiProvider, ce *callError) error {
	if ce.err != nil {
		return ce.err
	}
	if p.Compatible() {
		return errors.New(ce.message())
	}
	switch {
	case ce.code == codeTooBig:
		return errUnreadable(p)
	case ce.status != 0:
		return statusError(p, ce.status)
	case ce.code == codeTimeout:
		return fmt.Errorf("%s took too long to answer. Please try again.", displayName(p))
	}
	return fmt.Errorf("Could not reach %s. Check your internet connection.", displayName(p))
}
