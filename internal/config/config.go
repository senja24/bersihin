// Package config holds build-time constants: model ids and endpoints. They
// start from reference/cleanly/electron/config.ts.
package config

import "time"

// Model ids. Change them here and nowhere else.
const (
	ClaudeModel = "claude-opus-5-5"
	OpenAIModel = "gpt-5"
)

// Endpoints of the assistant's providers.
const (
	AnthropicURL     = "https://api.anthropic.com/v1/messages"
	AnthropicVersion = "2023-06-01"
	OpenAIURL        = "https://api.openai.com/v1/chat/completions"
)

// Assistant request limits.
const (
	AssistantTimeout   = 180 * time.Second
	AssistantMaxTokens = 8000
	// AssistantTestTimeout and AssistantTestMaxTokens limit the "Test
	// connection" request.
	AssistantTestTimeout   = 20 * time.Second
	AssistantTestMaxTokens = 256
	// AssistantMaxResponse is the largest answer read from a server;
	// AssistantMaxErrorBody the part of an error body that is looked at.
	AssistantMaxResponse  = 4 << 20
	AssistantMaxErrorBody = 4 << 10
)

// Names of the Keychain entries (service = the app identifier, chosen by MyGo).
const (
	ClaudeKeyName = "claude-api-key"
	OpenAIKeyName = "openai-api-key"

	ClaudeCompatKeyName = "claude-compatible-api-key"
	OpenAICompatKeyName = "openai-compatible-api-key"
)

// ProgressInterval is how often scan progress reaches the page.
const ProgressInterval = 120 * time.Millisecond

// Confirmation tokens and the scan snapshots they are checked against.
const (
	// ConfirmTTL is how long a confirmation token can be redeemed.
	ConfirmTTL = 2 * time.Minute
	// MaxGrants is how many tokens can be outstanding; the oldest is evicted.
	MaxGrants = 64
	// ScanTTL is how long a scan's results may be acted on.
	ScanTTL = 10 * time.Minute
	// MaxSnapshots is how many scans are remembered; the oldest is evicted.
	MaxSnapshots = 12
)

// ExternalLinks are the only pages System.OpenExternal opens: the AI vendors'
// API-key pages that Settings and the Assistant link to.
var ExternalLinks = []string{
	"https://console.anthropic.com/settings/keys",
	"https://platform.openai.com/api-keys",
}
