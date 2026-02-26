package router

import (
	"context"
	"net/http"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// aiClientWrapper wraps ai.Client to implement OpenAIClient
type aiClientWrapper struct {
	client ai.Client
}

func newAIClient(cfg *types.ProviderConfig) (*aiClientWrapper, error) {
	provider := ai.Provider(cfg.Provider)
	if provider == "" {
		provider = ai.ProviderOpenAI
	}
	client, err := ai.NewClient(ai.Config{
		Provider: provider,
		Config: openai.Config{
			APIKey:  cfg.Token,
			BaseURL: cfg.BaseURL,
		},
	})
	if err != nil {
		return nil, err
	}
	return &aiClientWrapper{client: client}, nil
}

func (c *aiClientWrapper) ListModels(ctx context.Context) (*openai.ModelsResponse, error) {
	return c.client.GetModels(ctx)
}

func (c *aiClientWrapper) ListModelsWithTimeout(ctx context.Context) (*openai.ModelsResponse, error) {
	return c.client.GetModels(ctx)
}

func (c *aiClientWrapper) ChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return c.client.ChatCompletion(ctx, *req)
}

func (c *aiClientWrapper) StreamChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) *openai.ChatStream {
	return c.client.StreamChatCompletion(ctx, *req)
}

func (c *aiClientWrapper) CreateEmbedding(ctx context.Context, req *openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return c.client.CreateEmbedding(ctx, *req)
}

// RawStream returns a raw HTTP response for streaming (not used with ai.Client)
func (c *aiClientWrapper) RawStream(ctx context.Context, req *openai.ChatCompletionRequest) (*http.Response, error) {
	return nil, nil
}
