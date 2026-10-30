package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dev.agus.bersihin/internal/model"
)

// Service answers questions. The provider comes from the settings, the key
// from the Keychain.
type Service struct {
	Home string
	// Provider returns the chosen backend; Key its API key ("" when none).
	Provider func() model.AiProvider
	Key      func(model.AiProvider) (string, error)

	HTTP *http.Client
	// AnthropicURL and OpenAIURL override the endpoints of internal/config (tests).
	AnthropicURL string
	OpenAIURL    string
}

// Ask sends the question and the (redacted) context to the chosen provider
// and returns its checked answer.
func (s *Service) Ask(ctx context.Context, question string, c model.AiContext) (model.AiAdvice, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return model.AiAdvice{}, errors.New("Type a question for the assistant first.")
	}
	if len([]rune(q)) > maxQuestion {
		return model.AiAdvice{}, fmt.Errorf("Please keep your question under %d characters.", maxQuestion)
	}
	provider := s.Provider()
	key, err := s.Key(provider)
	if err != nil {
		return model.AiAdvice{}, err
	}
	if key == "" {
		if provider == model.ProviderClaude {
			return model.AiAdvice{}, errors.New("Add your Claude API key in Settings to use the assistant.")
		}
		return model.AiAdvice{}, errors.New("Add your ChatGPT (OpenAI) API key in Settings to use the assistant.")
	}

	data, known := BuildContext(s.Home, c)
	user, err := UserMessage(s.Home, q, data)
	if err != nil {
		return model.AiAdvice{}, err
	}
	req := request{apiKey: key, system: SystemPrompt, user: user}
	var raw []byte
	if provider == model.ProviderClaude {
		raw, err = s.askClaude(ctx, req)
	} else {
		raw, err = s.askOpenAI(ctx, req)
	}
	if err != nil {
		return model.AiAdvice{}, err
	}
	return ParseAdvice(s.Home, raw, known, provider)
}
