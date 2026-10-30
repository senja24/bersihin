package api

import (
	"context"

	"dev.agus.bersihin/internal/model"
)

// Settings are the user's preferences. API keys live in the Keychain; the
// page only learns whether one is stored.
type Settings struct{ c *core }

// Get returns the settings.
func (s *Settings) Get() model.Settings { return s.c.settings.Get() }

// Set changes the fields that are set and returns the settings.
func (s *Settings) Set(patch model.SettingsPatch) (model.Settings, error) {
	return s.c.settings.Patch(patch)
}

// SetApiKey stores the provider's API key in the Keychain, or removes it when
// key is null or empty, and returns the settings. forBaseUrl is the address of
// a compatible provider as the page shows it (ignored for Claude and ChatGPT
// and when removing): the key is bound to the saved address, so the call is
// refused when that is another one. Save the address first, then the key.
func (s *Settings) SetApiKey(provider model.AiProvider, key *string, forBaseUrl string) (model.Settings, error) {
	return s.c.settings.SetKey(provider, key, forBaseUrl)
}

// Assistant asks Claude, ChatGPT or a compatible server what is safe to remove.
type Assistant struct{ c *core }

// Ask sends the question and the context (names, paths and sizes, with the
// home folder shown as ~) to the provider chosen in the settings.
func (a *Assistant) Ask(ctx context.Context, question string, data model.AiContext) (model.AiAdvice, error) {
	return a.c.assistant.Ask(ctx, question, data)
}

// TestConnection sends one tiny request to the provider with its saved API
// key and reports how it went: ok, or a code and a message that explain what
// to fix. draft may carry an address and model the user is still editing; its
// address must be the saved one (save a changed address first), so a stored
// key never goes to an address that was not saved. The page never sends or receives a key.
func (a *Assistant) TestConnection(ctx context.Context, provider model.AiProvider, draft *model.AiEndpointPatch) (model.AiTestResult, error) {
	return a.c.assistant.TestConnection(ctx, provider, draft)
}
