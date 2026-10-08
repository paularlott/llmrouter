package router

import (
	"net/http"
	"sync"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// responseStore holds the emulated Responses API responses of every provider
// client this router creates, so they are kept as long as the router's
// responses index ([responses] ttl_days) rather than the library default.
// Clients still only see responses created with their own provider, base URL
// and API key.
var (
	responseStoreMu sync.RWMutex
	responseStore   openai.ResponseStore
)

// setResponseStore sets up the shared response store from the [responses]
// config: responses are kept for its TTL after their last use, within its
// count and memory limits (least recently used dropped first).
func setResponseStore(cfg types.ResponsesConfig) {
	maxBytes := cfg.MaxMemoryMB
	if maxBytes > 0 {
		maxBytes <<= 20
	}
	responseStoreMu.Lock()
	defer responseStoreMu.Unlock()
	responseStore = openai.NewMemoryResponseStore(openai.MemoryResponseStoreOptions{
		TTL:          cfg.TTL(),
		MaxResponses: cfg.MaxResponses,
		MaxBytes:     maxBytes,
	})
}

func currentResponseStore() openai.ResponseStore {
	responseStoreMu.RLock()
	defer responseStoreMu.RUnlock()
	return responseStore
}

func newAIClient(cfg *types.ProviderConfig) (ai.Client, error) {
	return newAIClientWithHeaders(cfg, nil)
}

func newAIClientWithHeaders(cfg *types.ProviderConfig, extraHeaders http.Header) (ai.Client, error) {
	provider := ai.Provider(cfg.Provider)
	if provider == "" {
		provider = ai.ProviderOpenAI
	}
	return ai.NewClient(ai.Config{
		Provider: provider,
		Config: openai.Config{
			APIKey:        cfg.Token,
			BaseURL:       cfg.BaseURL,
			ExtraHeaders:  extraHeaders,
			ResponseStore: currentResponseStore(),
		},
	})
}
