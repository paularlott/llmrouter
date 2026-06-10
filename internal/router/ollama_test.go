package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

type ollamaEmbeddingClient struct {
	mockProviderClient
	LastEmbeddingReq openai.EmbeddingRequest
}

func (c *ollamaEmbeddingClient) CreateEmbedding(ctx context.Context, req openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	c.LastEmbeddingReq = req
	return &openai.EmbeddingResponse{
		Model: req.Model,
		Data: []openai.Embedding{
			{Object: "embedding", Index: 0, Embedding: []float64{0.1, 0.2}},
			{Object: "embedding", Index: 1, Embedding: []float64{0.3, 0.4}},
		},
		Usage: openai.Usage{PromptTokens: 3},
	}, nil
}

func newOllamaHandlerTestRouter(client ai.Client) *Router {
	r := &Router{
		Providers: map[string]*Provider{
			"mock-provider": {
				Name:         "mock-provider",
				ProviderType: "openai",
				Client:       client,
				Enabled:      true,
				Weight:       1,
				Models:       []string{"llama3.2"},
			},
		},
		ModelMap:  map[string][]string{"llama3.2": {"mock-provider"}},
		ModelTags: map[string][]string{},
		logger:    &testLogger{},
	}
	r.Providers["mock-provider"].Healthy.Store(true)
	return r
}

func TestHandleOllamaVersion(t *testing.T) {
	r := newOllamaHandlerTestRouter(&mockProviderClient{})
	req := httptest.NewRequest("GET", "/ollama/api/version", nil)
	rec := httptest.NewRecorder()

	r.HandleOllamaVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["version"] != ollamaAPIVersion {
		t.Fatalf("want version %q, got %q", ollamaAPIVersion, got["version"])
	}
}

func TestHandleOllamaTags_IncludesCapabilities(t *testing.T) {
	r := newOllamaHandlerTestRouter(&mockProviderClient{})
	req := httptest.NewRequest("GET", "/ollama/api/tags", nil)
	rec := httptest.NewRecorder()

	r.HandleOllamaTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got struct {
		Models []struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Models) != 1 {
		t.Fatalf("want 1 model, got %d", len(got.Models))
	}
	if !hasCapability(got.Models[0].Capabilities, "tools") || !hasCapability(got.Models[0].Capabilities, "vision") {
		t.Fatalf("expected tools and vision capabilities, got %#v", got.Models[0].Capabilities)
	}
}

func TestHandleOllamaChat_RoutesThroughOpenAIShape(t *testing.T) {
	client := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			Model: "llama3.2",
			Choices: []openai.Choice{
				{Message: openai.Message{Content: "hello from backend"}, FinishReason: "stop"},
			},
			Usage: &openai.Usage{PromptTokens: 2, CompletionTokens: 3},
		},
	}
	r := newOllamaHandlerTestRouter(client)

	body := []byte(`{"model":"llama3.2","stream":false,"messages":[{"role":"user","content":"hi"}],"options":{"temperature":0.4,"num_predict":25}}`)
	req := httptest.NewRequest("POST", "/ollama/api/chat", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	r.HandleOllamaChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if client.LastReq.Model != "llama3.2" {
		t.Fatalf("want routed model llama3.2, got %q", client.LastReq.Model)
	}
	if len(client.LastReq.Messages) != 1 || client.LastReq.Messages[0].GetContentAsString() != "hi" {
		t.Fatalf("unexpected routed messages: %#v", client.LastReq.Messages)
	}
	if client.LastReq.Temperature == nil || *client.LastReq.Temperature != 0.4 {
		t.Fatalf("temperature option was not forwarded")
	}
	if client.LastReq.MaxCompletionTokens != 25 {
		t.Fatalf("want num_predict mapped to 25 max tokens, got %d", client.LastReq.MaxCompletionTokens)
	}

	var got ollamaChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Message.Content != "hello from backend" || !got.Done {
		t.Fatalf("unexpected ollama chat response: %#v", got)
	}
}

func hasCapability(capabilities []string, capability string) bool {
	for _, got := range capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

func TestHandleOllamaGenerate_RoutesPromptAsUserMessage(t *testing.T) {
	client := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			Model:   "llama3.2",
			Choices: []openai.Choice{{Message: openai.Message{Content: "generated"}, FinishReason: "stop"}},
		},
	}
	r := newOllamaHandlerTestRouter(client)

	body := []byte(`{"model":"llama3.2","stream":false,"system":"be terse","prompt":"say hi"}`)
	req := httptest.NewRequest("POST", "/ollama/api/generate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	r.HandleOllamaGenerate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if len(client.LastReq.Messages) != 2 {
		t.Fatalf("want system and user messages, got %#v", client.LastReq.Messages)
	}
	if client.LastReq.Messages[0].Role != "system" || client.LastReq.Messages[0].GetContentAsString() != "be terse" {
		t.Fatalf("unexpected system message: %#v", client.LastReq.Messages[0])
	}
	if client.LastReq.Messages[1].Role != "user" || client.LastReq.Messages[1].GetContentAsString() != "say hi" {
		t.Fatalf("unexpected user message: %#v", client.LastReq.Messages[1])
	}

	var got ollamaGenerateResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Response != "generated" || !got.Done {
		t.Fatalf("unexpected ollama generate response: %#v", got)
	}
}

func TestHandleOllamaEmbed_MapsEmbeddingResponse(t *testing.T) {
	client := &ollamaEmbeddingClient{}
	r := newOllamaHandlerTestRouter(client)

	body := []byte(`{"model":"llama3.2","input":["one","two"],"dimensions":2}`)
	req := httptest.NewRequest("POST", "/ollama/api/embed", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	r.HandleOllamaEmbed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if client.LastEmbeddingReq.Model != "llama3.2" || client.LastEmbeddingReq.Dimensions != 2 {
		t.Fatalf("unexpected embedding request: %#v", client.LastEmbeddingReq)
	}

	var got struct {
		Model      string      `json:"model"`
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Model != "llama3.2" || len(got.Embeddings) != 2 || got.Embeddings[1][1] != 0.4 {
		t.Fatalf("unexpected ollama embed response: %#v", got)
	}
}
