package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestHandleLoginRoleDispatch covers the role-aware login flow: admin
// password yields RoleAdmin, chat password yields RoleChat, and the role is
// observable via sessionRole.
func TestHandleLoginRoleDispatch(t *testing.T) {
	cfg := &types.Config{}
	cfg.Server.AdminPassword = "secret-admin"
	cfg.Server.ChatPassword = "secret-chat"

	a := New(cfg, nil, nil, nil, nil, nil, nil, nil, nil, false, nil, nil)
	if a == nil {
		t.Fatal("New returned nil")
	}

	cases := []struct {
		name     string
		password string
		want     Role
	}{
		{"admin password authenticates admin", "secret-admin", RoleAdmin},
		{"chat password authenticates chat-only", "secret-chat", RoleChat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"password":%q}`, c.password)
			req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			rec := httptest.NewRecorder()
			a.HandleLogin(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status: %d (%s)", rec.Code, rec.Body.String())
			}
			var resp struct {
				Token string `json:"token"`
				Role  string `json:"role"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp.Role != string(c.want) {
				t.Fatalf("role: want %s got %s", c.want, resp.Role)
			}
			if a.sessionRole(resp.Token) != c.want {
				t.Fatalf("sessionRole mismatch for token %s", resp.Token)
			}
		})
	}
}

// TestHandleLoginRejectsWrongPassword ensures unknown passwords are rejected
// regardless of which password slot they target.
func TestHandleLoginRejectsWrongPassword(t *testing.T) {
	cfg := &types.Config{}
	cfg.Server.AdminPassword = "admin"
	cfg.Server.ChatPassword = "chat"
	a := New(cfg, nil, nil, nil, nil, nil, nil, nil, nil, false, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/login",
		strings.NewReader(`{"password":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.HandleLogin(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec.Code)
	}
}

// TestRequireAuthRejectsChatRole proves the role-aware gate works: a chat-only
// session must not pass admin API auth.
func TestRequireAuthRejectsChatRole(t *testing.T) {
	a := &Admin{
		password:     "admin",
		chatPassword: "chat",
		sessions:     map[string]*Session{},
	}
	// Create sessions for each role.
	tokAdmin, _ := a.createSession(RoleAdmin)
	tokChat, _ := a.createSession(RoleChat)

	called := false
	h := a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	// Admin token passes.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/anything", nil)
	req.Header.Set("Authorization", "Bearer "+tokAdmin)
	h(rec, req)
	if !called {
		t.Fatal("admin session should pass requireAuth")
	}

	// Chat token is rejected.
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/api/anything", nil)
	req.Header.Set("Authorization", "Bearer "+tokChat)
	h(rec, req)
	if called {
		t.Fatal("chat session should not pass requireAuth")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec.Code)
	}
}

// TestRequireReadOnlyPageAuthAcceptsBothRoles covers the read-only MCP page
// gate: both admin and chat sessions pass; unauthenticated requests redirect.
func TestRequireReadOnlyPageAuthAcceptsBothRoles(t *testing.T) {
	a := &Admin{
		password:     "admin",
		chatPassword: "chat",
		sessions:     map[string]*Session{},
	}
	tokAdmin, _ := a.createSession(RoleAdmin)
	tokChat, _ := a.createSession(RoleChat)

	called := false
	h := a.requireReadOnlyPageAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	for _, tok := range []string{tokAdmin, tokChat} {
		called = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/mcp-servers", nil)
		req.AddCookie(&http.Cookie{Name: "admin_session", Value: tok})
		h(rec, req)
		if !called {
			t.Fatalf("token %s should pass read-only auth", tok)
		}
	}

	// No cookie -> redirect to login.
	called = false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/mcp-servers", nil)
	h(rec, req)
	if called {
		t.Fatal("unauthenticated should not pass")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("want redirect got %d", rec.Code)
	}
}

// TestChatAuthMiddlewareReturnsNilWhenNoPasswords ensures that when neither
// password is configured, the chat middleware is nil (host mounts open chat).
func TestChatAuthMiddlewareReturnsNilWhenNoPasswords(t *testing.T) {
	a := &Admin{
		sessions: map[string]*Session{},
	}
	if a.ChatEnabled() {
		t.Fatal("ChatEnabled should be false when no passwords set")
	}
	if a.ChatAuthMiddleware() != nil {
		t.Fatal("ChatAuthMiddleware should be nil when no passwords set")
	}
}

// TestChatEnabledReportsTrueWhenEitherPasswordSet covers the truth table.
func TestChatEnabledReportsTrueWhenEitherPasswordSet(t *testing.T) {
	cases := []struct {
		admin, chat string
		want        bool
	}{
		{"", "", false},
		{"a", "", true},
		{"", "c", true},
		{"a", "c", true},
	}
	for _, c := range cases {
		a := &Admin{password: c.admin, chatPassword: c.chat, sessions: map[string]*Session{}}
		if got := a.ChatEnabled(); got != c.want {
			t.Errorf("ChatEnabled(admin=%q,chat=%q) = %v want %v", c.admin, c.chat, got, c.want)
		}
	}
}

// TestChatAuthMiddlewareNilWhenNoChatPassword locks in the rule that the chat
// UI is open whenever chat_password is empty — even if admin_password is set.
// This is what lets an admin walk straight into /chat without re-authing when
// no chat-specific password has been configured.
func TestChatAuthMiddlewareNilWhenNoChatPassword(t *testing.T) {
	cases := []struct {
		admin, chat string
		wantNil     bool
	}{
		{"", "", true},    // no auth at all → open
		{"a", "", true},   // admin only, no chat password → open (the case the user hit)
		{"", "c", false},  // chat password only → gated
		{"a", "c", false}, // both → gated
	}
	for _, c := range cases {
		a := &Admin{password: c.admin, chatPassword: c.chat, sessions: map[string]*Session{}}
		got := a.ChatAuthMiddleware()
		if (got == nil) != c.wantNil {
			t.Errorf("ChatAuthMiddleware(admin=%q,chat=%q) nil=%v want %v", c.admin, c.chat, got == nil, c.wantNil)
		}
	}
}

// TestChatAuthMiddlewareRedirectsToAdminLogin confirms the redirect target
// is the shared /admin/login (which knows how to handle both password types)
// rather than a non-existent /chat/login.
func TestChatAuthMiddlewareRedirectsToAdminLogin(t *testing.T) {
	a := &Admin{
		password:     "admin",
		chatPassword: "chat",
		sessions:     map[string]*Session{},
	}
	mw := a.ChatAuthMiddleware()
	if mw == nil {
		t.Fatal("expected non-nil middleware when chat_password is set")
	}

	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for unauthenticated request")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("want redirect got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/login") {
		t.Fatalf("redirect location: %q (want /admin/login?...)", loc)
	}
}
