package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
)

// TestNativeOllamaAPIRoutesWired proves the /api/* routes are registered on the
// real mux (not just callable as handlers), so Ollama clients pointed at the
// server root work out of the box. It builds the actual router and drives it
// over HTTP. No token is set, so auth is a no-op.
func TestNativeOllamaAPIRoutesWired(t *testing.T) {
	r, err := NewRouter(&types.Config{
		Server:    types.ServerConfig{Host: "127.0.0.1", Port: 0},
		Providers: []types.ProviderConfig{},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := srv.Client()

	// Register a single provider + model on the already-built router so the
	// POST handlers have something real to act on (proving full dispatch +
	// translation through the mux, not just route matching).
	p := &Provider{Name: "p", ProviderType: "openai", Client: &mockProviderClient{}, Enabled: true, Weight: 1, Models: []string{"m"}}
	p.Healthy.Store(true)
	r.Providers["p"] = p
	r.ModelMap["m"] = []string{"p"}
	r.ModelContext["m"] = 4096

	t.Run("version", func(t *testing.T) {
		resp, err := client.Get(srv.URL + "/api/version")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("/api/version: %d %s", resp.StatusCode, string(b))
		}
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got["version"] != ollamaAPIVersion {
			t.Fatalf("/api/version body = %#v, want version %q", got, ollamaAPIVersion)
		}
	})

	t.Run("tags", func(t *testing.T) {
		resp, err := client.Get(srv.URL + "/api/tags")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/api/tags: %d", resp.StatusCode)
		}
	})

	t.Run("show dispatches + returns JSON for a known model", func(t *testing.T) {
		resp, err := client.Post(srv.URL+"/api/show", "application/json", bytes.NewReader([]byte(`{"model":"m"}`)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("/api/show: %d %s", resp.StatusCode, string(b))
		}
		// A 200 JSON body with model_info is only producible by HandleOllamaShow,
		// so this proves the POST route dispatched through the mux.
		var got struct {
			ModelInfo map[string]any `json:"model_info"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("/api/show decode: %v", err)
		}
		if _, ok := got.ModelInfo["context_length"]; !ok {
			t.Fatalf("/api/show missing model_info.context_length: %#v", got.ModelInfo)
		}
	})

	t.Run("unsupported /api/pull dispatches to HandleUnsupported", func(t *testing.T) {
		// Distinguish a genuine dispatch (body "Not supported") from a mux/catch-all 404.
		resp, err := client.Post(srv.URL+"/api/pull", "application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if !bytes.Contains(b, []byte("Not supported")) {
			t.Fatalf("/api/pull body = %q, want it to contain \"Not supported\" (route did not dispatch to HandleUnsupported)", string(b))
		}
	})
}
