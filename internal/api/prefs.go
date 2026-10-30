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
// key is null or empty, and returns the settings.
func (s *Settings) SetApiKey(provider model.AiProvider, key *string) (model.Settings, error) {
	return s.c.settings.SetKey(provider, key)
}

// Assistant asks Claude or ChatGPT what is safe to remove.
type Assistant struct{ c *core }

// Ask sends the question and the context (names, paths and sizes, with the
// home folder shown as ~) to the provider chosen in the settings.
func (a *Assistant) Ask(ctx context.Context, question string, data model.AiContext) (model.AiAdvice, error) {
	return a.c.assistant.Ask(ctx, question, data)
}
