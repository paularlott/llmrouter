package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/paularlott/llmrouter/internal/storage"
)

// An MCP server whose authorization server is elsewhere (found through its protected resource metadata, RFC 9728):
// the sign-in names the server as the resource (RFC 8707) when authorizing and when exchanging the code, as such an
// authorization server needs it to issue a token for the server.
func TestOAuthSignInNamesTheResource(t *testing.T) {
	var tokenResource string
	_, resource := newFakeOAuthServers(t, true, &tokenResource)

	a := &Admin{sessions: map[string]*Session{}, mcpStorage: storage.NewMemoryMCPStorage(), mcpStorageWritable: true}

	body := `{"namespace":"ff3","url":"` + resource + `?show_all=true","callback_base":"https://llmrouter.test"}`
	rec := httptest.NewRecorder()
	a.HandleOAuthStart(rec, httptest.NewRequest(http.MethodPost, "/admin/oauth/start", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started struct {
		AuthURL string `json:"auth_url"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&started)
	authURL, err := url.Parse(started.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	q := authURL.Query()
	if q.Get("resource") != resource || q.Get("client_id") != "c_registered" {
		t.Fatalf("authorization request: %s", started.AuthURL)
	}

	rec = httptest.NewRecorder()
	callback := "https://llmrouter.test/admin/oauth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=the-code"
	a.HandleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, callback, nil))
	if rec.Code >= 400 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if tokenResource != resource {
		t.Errorf("token request resource = %q, want %q", tokenResource, resource)
	}
	saved, err := a.mcpStorage.Get(context.Background(), "ff3")
	if err != nil || saved.OAuthRefreshToken != "rt" || saved.OAuthClientID != "c_registered" {
		t.Errorf("saved server = %+v, %v", saved, err)
	}
	// The resource is persisted so token refreshes name it too (RFC 8707).
	if saved != nil && saved.OAuthResource != resource {
		t.Errorf("saved OAuthResource = %q, want %q", saved.OAuthResource, resource)
	}
}

// An authorization server that does not advertise PKCE S256 cannot be used
// (the MCP spec requires it); the admin API says so plainly.
func TestOAuthStartRejectsServerWithoutPKCE(t *testing.T) {
	_, resource := newFakeOAuthServers(t, false, nil)
	a := &Admin{sessions: map[string]*Session{}, mcpStorage: storage.NewMemoryMCPStorage(), mcpStorageWritable: true}
	body := `{"namespace":"nopkce","url":"` + resource + `","callback_base":"https://llmrouter.test"}`
	rec := httptest.NewRecorder()
	a.HandleOAuthStart(rec, httptest.NewRequest(http.MethodPost, "/admin/oauth/start", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PKCE") {
		t.Errorf("error does not mention PKCE: %s", rec.Body.String())
	}
}

// newFakeOAuthServers starts an authorization server and an MCP server that
// points at it through its protected resource metadata. It returns the AS
// and the MCP server's resource URL.
func newFakeOAuthServers(t *testing.T, pkce bool, tokenResource *string) (*httptest.Server, string) {
	t.Helper()
	var as *httptest.Server
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/o/org-1":
			meta := map[string]any{
				"issuer":                 as.URL + "/o/org-1",
				"authorization_endpoint": as.URL + "/o/org-1/oauth/authorize",
				"token_endpoint":         as.URL + "/o/org-1/oauth/token",
				"registration_endpoint":  as.URL + "/o/org-1/oauth/register",
			}
			if pkce {
				meta["code_challenge_methods_supported"] = []string{"S256"}
			}
			_ = json.NewEncoder(w).Encode(meta)
		case "/o/org-1/oauth/register":
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "c_registered"})
		case "/o/org-1/oauth/token":
			_ = r.ParseForm()
			if tokenResource != nil {
				*tokenResource = r.PostForm.Get("resource")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "refresh_token": "rt"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(as.Close)

	var mcpServer *httptest.Server
	mcpServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcpServer.URL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              mcpServer.URL + "/mcp",
				"authorization_servers": []string{as.URL + "/o/org-1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mcpServer.Close)
	return as, mcpServer.URL + "/mcp"
}
