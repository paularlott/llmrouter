package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
)

// writeRouter writes a smart-router <name>.toml (no .py => pure alias router).
func writeRouter(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func newSmartRouterTestRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir := t.TempDir()
	r := &Router{
		Providers:    map[string]*Provider{},
		ModelMap:     map[string][]string{},
		ModelTags:    map[string][]string{},
		ModelContext: map[string]int{},
		config:       &types.Config{},
		logger:       &testLogger{},
	}
	return r, dir
}

// TestSmartRouterContextSize_TomlToOllamaAPI closes the whole loop: a
// context_size declared in routers/<name>.toml is loaded into ModelContext and
// served back through the Ollama /api/show endpoint. A router only exists when
// its .toml is present (there is no built-in "auto").
func TestSmartRouterContextSize_TomlToOllamaAPI(t *testing.T) {
	r, dir := newSmartRouterTestRouter(t)
	writeRouter(t, dir, "auto", "enabled = true\ndefault_model = \"gpt-4o\"\ncontext_size = 128000\n")

	mgr, err := newSmartRouterManager(dir, nil, r, &testLogger{})
	if err != nil {
		t.Fatalf("newSmartRouterManager: %v", err)
	}
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(mgr.Stop)
	r.smartRouters = mgr // mirrors NewRouter wiring, so smartRouterFor("auto") resolves

	// The loader populates ModelContext from the toml's context_size.
	r.ModelMapMu.RLock()
	got := r.ModelContext["auto"]
	r.ModelMapMu.RUnlock()
	if got != 128000 {
		t.Fatalf("ModelContext[auto] = %d, want 128000", got)
	}

	// And it is served via /api/show as model_info.context_length.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/show", bytes.NewReader([]byte(`{"model":"auto"}`)))
	r.HandleOllamaShow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/show: %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cl, _ := resp.ModelInfo["context_length"].(float64)
	if int(cl) != 128000 {
		t.Fatalf("served context_length = %v, want 128000", resp.ModelInfo["context_length"])
	}
}

// TestSmartRouterContextSize_NoTomlNoModel proves the complementary case: with
// no auto.toml there is no "auto" model at all, and the loader leaves
// ModelContext untouched.
func TestSmartRouterContextSize_NoTomlNoModel(t *testing.T) {
	r, dir := newSmartRouterTestRouter(t)
	// Only a "fred" router, no "auto".
	writeRouter(t, dir, "fred", "enabled = true\ncontext_size = 8192\n")

	mgr, err := newSmartRouterManager(dir, nil, r, &testLogger{})
	if err != nil {
		t.Fatalf("newSmartRouterManager: %v", err)
	}
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(mgr.Stop)

	r.ModelMapMu.RLock()
	autoCtx, autoOk := r.ModelContext["auto"]
	fredCtx := r.ModelContext["fred"]
	r.ModelMapMu.RUnlock()
	if autoOk || autoCtx != 0 {
		t.Fatalf("ModelContext[auto] = %d (ok=%v); want absent — no auto.toml exists", autoCtx, autoOk)
	}
	if fredCtx != 8192 {
		t.Fatalf("ModelContext[fred] = %d, want 8192", fredCtx)
	}
}
