package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

// contextDiscoveryClient is a mock provider client whose GetModels reports a
// context window per model, exercising the discovery -> ModelContext -> served
// path end to end without needing a live upstream.
type contextDiscoveryClient struct {
	mockProviderClient
	Models    []openai.Model
	gotCalled bool
}

func (c *contextDiscoveryClient) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	c.gotCalled = true
	return &openai.ModelsResponse{Object: "list", Data: c.Models}, nil
}

func newContextTestRouter() *Router {
	r := &Router{
		Providers:   map[string]*Provider{},
		ModelMap:    map[string][]string{},
		ModelTags:   map[string][]string{},
		ModelContext: map[string]int{},
		logger:      &testLogger{},
	}
	return r
}

// TestOllamaShow_ServesContextLength proves /api/show emits the resolved
// context window as model_info.context_length (the whole point of routing
// Ollama clients through the gateway).
func TestOllamaShow_ServesContextLength(t *testing.T) {
	r := newContextTestRouter()
	r.Providers["p"] = &Provider{Name: "p", ProviderType: "openai", Enabled: true, Weight: 1, Models: []string{"ctx-model"}}
	r.Providers["p"].Healthy.Store(true)
	r.ModelMap["ctx-model"] = []string{"p"}
	r.ModelContext["ctx-model"] = 131072 // resolved context

	body := []byte(`{"model":"ctx-model"}`)
	req := httptest.NewRequest("POST", "/api/show", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.HandleOllamaShow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ModelInfo map[string]any `json:"model_info"`
		Parameters string        `json:"parameters"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cl, _ := got.ModelInfo["context_length"].(float64)
	if int(cl) != 131072 {
		t.Fatalf("context_length = %v, want 131072 (full: %#v)", got.ModelInfo["context_length"], got)
	}
	if got.Parameters == "" {
		t.Fatalf("expected num_ctx in parameters, got empty")
	}
}

// TestOllamaTags_ServesContextLength proves /api/tags carries each model's
// discovered context length, flowing discovery -> ModelContext -> the tags list.
func TestOllamaTags_ServesContextLength(t *testing.T) {
	client := &contextDiscoveryClient{
		Models: []openai.Model{
			{ID: "ctx-model", Object: "model", ContextWindow: 8192},
		},
	}
	r := newContextTestRouter()
	r.Providers["p"] = &Provider{
		Name: "p", ProviderType: "openai", Client: client,
		Enabled: true, Weight: 1, Models: []string{"ctx-model"},
	}
	r.Providers["p"].Healthy.Store(true)
	r.ModelMap["ctx-model"] = []string{"p"}

	req := httptest.NewRequest("GET", "/api/tags", nil)
	rec := httptest.NewRecorder()
	r.HandleOllamaTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	if !client.gotCalled {
		t.Fatalf("provider GetModels was not invoked by /api/tags refresh")
	}

	var got struct {
		Models []struct {
			Name      string         `json:"name"`
			ModelInfo map[string]any `json:"model_info"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Models) != 1 || got.Models[0].Name != "ctx-model" {
		t.Fatalf("want 1 model ctx-model, got %#v", got.Models)
	}
	cl, _ := got.Models[0].ModelInfo["context_length"].(float64)
	if int(cl) != 8192 {
		t.Fatalf("tags context_length = %v, want 8192", got.Models[0].ModelInfo["context_length"])
	}
}

// TestOllamaTags_ContextFromProviderDefault proves the fallback chain reaches
// /api/tags: when discovery yields nothing, the per-provider default applies.
func TestOllamaTags_ContextFromProviderDefault(t *testing.T) {
	client := &contextDiscoveryClient{
		Models: []openai.Model{{ID: "nodisc", Object: "model"}}, // no ContextWindow
	}
	r := newContextTestRouter()
	r.Providers["p"] = &Provider{
		Name: "p", ProviderType: "openai", Client: client,
		Enabled: true, Weight: 1, Models: []string{"nodisc"},
		DefaultContextSize: 32768,
	}
	r.Providers["p"].Healthy.Store(true)
	r.ModelMap["nodisc"] = []string{"p"}

	req := httptest.NewRequest("GET", "/api/tags", nil)
	rec := httptest.NewRecorder()
	r.HandleOllamaTags(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got struct {
		Models []struct {
			ModelInfo map[string]any `json:"model_info"`
		} `json:"models"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	cl, _ := got.Models[0].ModelInfo["context_length"].(float64)
	if int(cl) != 32768 {
		t.Fatalf("provider-default context_length = %v, want 32768", got.Models[0].ModelInfo["context_length"])
	}
}
