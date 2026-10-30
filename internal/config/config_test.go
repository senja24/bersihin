package config

import (
	"testing"
	"time"
)

// A non-streaming request is answered only when the whole reply is written,
// so the timeout has to cover max_tokens at a slow generation speed.
func TestAssistantTimeoutCoversMaxTokens(t *testing.T) {
	const slowTokensPerSecond = 50
	need := time.Duration(AssistantMaxTokens/slowTokensPerSecond) * time.Second
	if AssistantTimeout < need {
		t.Fatalf("timeout %v is shorter than %d tokens at %d tokens/s (%v)", AssistantTimeout, AssistantMaxTokens, slowTokensPerSecond, need)
	}
}
