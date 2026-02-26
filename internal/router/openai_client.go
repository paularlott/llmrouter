package router

import (
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

func newAIClient(cfg *types.ProviderConfig) (ai.Client, error) {
	provider := ai.Provider(cfg.Provider)
	if provider == "" {
		provider = ai.ProviderOpenAI
	}
	return ai.NewClient(ai.Config{
		Provider: provider,
		Config: openai.Config{
			APIKey:  cfg.Token,
			BaseURL: cfg.BaseURL,
		},
	})
}
