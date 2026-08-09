package router

import (
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai/ollama"
	"github.com/paularlott/mcp/ai/openai"
)

// TestNewAIClientOllamaUsesNativeClient proves that a provider configured as
// ollama — whether from a config file or created via the admin UI (both flow
// through newAIClient -> ai.NewClient) — uses the native ollama client, not the
// OpenAI-compat client. This is the guarantee that chat/embeddings/models go to
// Ollama's /api/* upstream rather than its /v1 shim.
func TestNewAIClientOllamaUsesNativeClient(t *testing.T) {
	c, err := newAIClient(&types.ProviderConfig{
		Name:     "local",
		Provider: "ollama",
		BaseURL:  "http://127.0.0.1:11434",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("newAIClient(ollama): %v", err)
	}
	if c.Provider() != "ollama" {
		t.Fatalf("Provider() = %q, want ollama", c.Provider())
	}
	if _, ok := c.(*ollama.Client); !ok {
		t.Fatalf("newAIClient(ollama) returned %T, want *ollama.Client", c)
	}
}

// TestNewAIClientOpenAICompatibleStaysOnOpenAI guards the inverse: the other
// OpenAI-compatible providers must keep using the OpenAI client.
func TestNewAIClientOpenAICompatibleStaysOnOpenAI(t *testing.T) {
	for _, provider := range []string{"openai", "mistral", "zai"} {
		c, err := newAIClient(&types.ProviderConfig{Name: "p", Provider: provider, Token: "k", Enabled: true})
		if err != nil {
			t.Fatalf("newAIClient(%s): %v", provider, err)
		}
		if _, ok := c.(*openai.Client); !ok {
			t.Fatalf("newAIClient(%s) returned %T, want *openai.Client", provider, c)
		}
	}
}
