package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
)

// newTestAdmin builds an Admin wired with canned callbacks for resources and
// prompts. It skips the template renderer (these API handlers don't touch it)
// and bypasses auth for direct handler invocation.
func newTestAdmin(t *testing.T, resources []ResourceInfo, prompts []PromptInfo) *Admin {
	t.Helper()
	a := &Admin{
		password: "test",
		sessions: map[string]*Session{},
		getMCPResources: func(namespace string) ([]ResourceInfo, error) {
			if namespace != "demo" {
				return nil, nil
			}
			return resources, nil
		},
		getMCPPrompts: func(namespace string) ([]PromptInfo, error) {
			if namespace != "demo" {
				return nil, nil
			}
			return prompts, nil
		},
	}
	// Pre-create a session token so requireAuth passes. The token is "test-token".
	a.sessions["test-token"] = &Session{Token: "test-token"}
	return a
}

func TestHandleGetMCPServerResources(t *testing.T) {
	canned := []ResourceInfo{
		{URI: "docs://readme.md", Name: "readme", Description: "Read me", MimeType: "text/markdown"},
		{URI: "greeting://{name}", Template: true, Name: "greeting", MimeType: "text/plain"},
	}
	a := newTestAdmin(t, canned, nil)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/mcp-servers/demo/resources", nil)
	req.SetPathValue("namespace", "demo")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	a.HandleGetMCPServerResources(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []ResourceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(got))
	}
	if got[0].URI != "docs://readme.md" || got[0].Template {
		t.Fatalf("static resource mismatch: %+v", got[0])
	}
	if got[1].URI != "greeting://{name}" || !got[1].Template {
		t.Fatalf("template mismatch: %+v", got[1])
	}
}

func TestHandleGetMCPServerResourcesEmptyWhenNoCallback(t *testing.T) {
	// Admin with no getMCPResources callback (e.g. router with no MCP server)
	// returns an empty list, not an error.
	a := &Admin{
		password: "test",
		sessions: map[string]*Session{"test-token": {Token: "test-token"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/mcp-servers/whatever/resources", nil)
	req.SetPathValue("namespace", "whatever")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	a.HandleGetMCPServerResources(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d", rec.Code)
	}
	var got []ResourceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
}

func TestHandleGetMCPServerPrompts(t *testing.T) {
	canned := []PromptInfo{
		{Name: "review", Description: "Review code", Arguments: []PromptArgument{{Name: "language", Required: true}}},
		{Name: "tips", Description: "Tips"},
	}
	a := newTestAdmin(t, nil, canned)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/mcp-servers/demo/prompts", nil)
	req.SetPathValue("namespace", "demo")
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	a.HandleGetMCPServerPrompts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []PromptInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(got))
	}
	if got[0].Name != "review" || len(got[0].Arguments) != 1 || !got[0].Arguments[0].Required {
		t.Fatalf("review prompt mismatch: %+v", got[0])
	}
}

func TestHandleGetMCPServerRequiresNamespace(t *testing.T) {
	a := newTestAdmin(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/mcp-servers//resources", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	a.HandleGetMCPServerResources(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestAdminNewWiresResourceAndPromptCallbacks verifies the constructor wires
// the new callbacks onto the struct. Smoke test against the long positional
// signature so a future reorder is caught.
func TestAdminNewWiresResourceAndPromptCallbacks(t *testing.T) {
	cfg := &types.Config{}
	cfg.Server.AdminPassword = "p"

	var gotResources []ResourceInfo
	var gotPrompts []PromptInfo

	a := New(cfg,
		func() *Stats { return nil },
		func() []ProviderInfo { return nil },
		func() []MCPServerInfo { return nil },
		func(string) ([]ToolInfo, error) { return nil, nil },
		func(namespace string) ([]ResourceInfo, error) {
			gotResources = append(gotResources, ResourceInfo{URI: namespace})
			return nil, nil
		},
		func(namespace string) ([]PromptInfo, error) {
			gotPrompts = append(gotPrompts, PromptInfo{Name: namespace})
			return nil, nil
		},
		func() []ModelInfo { return nil },
		nil, false, nil, nil,
	)
	if a == nil {
		t.Fatal("New returned nil")
	}
	if a.getMCPResources == nil || a.getMCPPrompts == nil {
		t.Fatal("resource/prompt callbacks not wired")
	}
	// Smoke-call the callbacks to confirm they're the ones we passed.
	_, _ = a.getMCPResources("ns-r")
	_, _ = a.getMCPPrompts("ns-p")
	if len(gotResources) != 1 || gotResources[0].URI != "ns-r" {
		t.Fatalf("resource callback not as wired: %+v", gotResources)
	}
	if len(gotPrompts) != 1 || gotPrompts[0].Name != "ns-p" {
		t.Fatalf("prompt callback not as wired: %+v", gotPrompts)
	}
}
