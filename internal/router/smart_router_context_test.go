package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
)

// TestResolvedContextLocked_FallbackForVirtualModels proves a virtual
// smart-router model with no backing provider still reports a context window
// (declared -> global default -> 4096 floor) — solving the "auto has no
// context size" case.
func TestResolvedContextLocked_FallbackForVirtualModels(t *testing.T) {
	t.Run("declared context wins", func(t *testing.T) {
		r := &Router{ModelContext: map[string]int{"auto": 200000}, config: &types.Config{}}
		if got := r.resolvedContextLocked("auto"); got != 200000 {
			t.Fatalf("got %d, want 200000", got)
		}
	})

	t.Run("undeclared -> global default", func(t *testing.T) {
		r := &Router{
			ModelContext: map[string]int{},
			config:       &types.Config{Server: types.ServerConfig{DefaultContextSize: 32000}},
		}
		if got := r.resolvedContextLocked("auto"); got != 32000 {
			t.Fatalf("got %d, want global 32000", got)
		}
	})

	t.Run("undeclared, no global -> 4096 floor", func(t *testing.T) {
		r := &Router{ModelContext: map[string]int{}, config: &types.Config{}}
		if got := r.resolvedContextLocked("auto"); got != defaultContextFloor {
			t.Fatalf("got %d, want %d", got, defaultContextFloor)
		}
	})
}

// TestHandleOllamaShow_VirtualModelFallback proves /api/show serves a context
// window for a virtual model via the fallback chain (no provider discovery).
func TestHandleOllamaShow_VirtualModelFallback(t *testing.T) {
	r := &Router{
		Providers:    map[string]*Provider{},
		ModelMap:     map[string][]string{"auto": {}}, // present so the handler doesn't 404
		ModelTags:    map[string][]string{},
		ModelContext: map[string]int{}, // "auto" deliberately absent
		config:       &types.Config{Server: types.ServerConfig{DefaultContextSize: 128000}},
		logger:       &testLogger{},
	}

	req := httptest.NewRequest("POST", "/api/show", bytes.NewReader([]byte(`{"model":"auto"}`)))
	rec := httptest.NewRecorder()
	r.HandleOllamaShow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cl, _ := got.ModelInfo["context_length"].(float64)
	if int(cl) != 128000 {
		t.Fatalf("auto context_length = %v, want 128000 (global default)", got.ModelInfo["context_length"])
	}
}
