package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/mcp"
)

// pendingOAuth holds state for an in-progress OAuth2 PKCE flow
type pendingOAuth struct {
	Namespace      string
	URL            string
	ToolVisibility string
	Enabled        bool
	RemoteSearch   bool
	ClientID       string
	TokenURL       string
	CodeVerifier   string
	SessionToken   string
	Reauth         bool
	CreatedAt      time.Time
}

var (
	pendingOAuthMu    sync.Mutex
	pendingOAuthStore = map[string]*pendingOAuth{}
)

func generateState() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}

func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// HandleOAuthStart initiates the PKCE flow for a new OAuth2 MCP server.
// POST /admin/api/mcp-servers/oauth/start
func (a *Admin) HandleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if a.mcpStorage == nil || !a.mcpStorageWritable {
		writeError(w, http.StatusBadRequest, "MCP server storage requires a configured storage path")
		return
	}

	var req struct {
		Namespace      string `json:"namespace"`
		URL            string `json:"url"`
		ToolVisibility string `json:"tool_visibility"`
		Enabled        bool   `json:"enabled"`
		RemoteSearch   bool   `json:"remote_search"`
		CallbackBase   string `json:"callback_base"`
		Reauth         bool   `json:"reauth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Namespace == "" || req.URL == "" || req.CallbackBase == "" {
		writeError(w, http.StatusBadRequest, "namespace, url and callback_base are required")
		return
	}
	if req.ToolVisibility == "" {
		req.ToolVisibility = "native"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	meta, err := mcp.DiscoverOAuthMeta(ctx, req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	callbackURL := strings.TrimSuffix(req.CallbackBase, "/") + "/admin/oauth/callback"

	// Register client dynamically if the server supports it
	clientID := "llmrouter"
	if meta.RegistrationEndpoint != "" {
		if id, err := mcp.RegisterOAuthClient(ctx, meta.RegistrationEndpoint, "llmrouter", callbackURL); err == nil {
			clientID = id
		}
	}

	state, err := generateState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate state")
		return
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate PKCE")
		return
	}

	pending := &pendingOAuth{
		Namespace:      req.Namespace,
		URL:            strings.TrimSuffix(req.URL, "/"),
		ToolVisibility: req.ToolVisibility,
		Enabled:        req.Enabled,
		RemoteSearch:   req.RemoteSearch,
		ClientID:       clientID,
		TokenURL:       meta.TokenEndpoint,
		CodeVerifier:   verifier,
		SessionToken:   a.getSessionFromCookie(r),
		Reauth:         req.Reauth,
		CreatedAt:      time.Now(),
	}

	pendingOAuthMu.Lock()
	// Prune stale entries (> 10 min)
	for k, v := range pendingOAuthStore {
		if time.Since(v.CreatedAt) > 10*time.Minute {
			delete(pendingOAuthStore, k)
		}
	}
	pendingOAuthStore[state] = pending
	pendingOAuthMu.Unlock()

	authURL, _ := url.Parse(meta.AuthorizationEndpoint)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", callbackURL)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	writeJSON(w, http.StatusOK, map[string]string{"auth_url": authURL.String()})
}

// HandleOAuthCallback handles the redirect back from the OAuth2 server.
// GET /admin/oauth/callback
func (a *Admin) HandleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	pendingOAuthMu.Lock()
	pending, ok := pendingOAuthStore[state]
	if ok {
		delete(pendingOAuthStore, state)
	}
	pendingOAuthMu.Unlock()

	if !ok {
		http.Error(w, "unknown or expired OAuth state", http.StatusBadRequest)
		return
	}

	callbackURL := fmt.Sprintf("%s://%s/admin/oauth/callback", scheme(r), r.Host)

	// Exchange code for tokens
	resp, err := http.PostForm(pending.TokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {callbackURL},
		"client_id":     {pending.ClientID},
		"code_verifier": {pending.CodeVerifier},
	})
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil || tokens.AccessToken == "" {
		http.Error(w, "invalid token response", http.StatusBadGateway)
		return
	}

	server := &storage.MCPServerConfig{
		Namespace:         pending.Namespace,
		URL:               pending.URL,
		AuthType:          "oauth2",
		OAuthClientID:     pending.ClientID,
		OAuthTokenURL:     pending.TokenURL,
		OAuthAccessToken:  tokens.AccessToken,
		OAuthRefreshToken: tokens.RefreshToken,
		Enabled:           pending.Enabled,
		ToolVisibility:    pending.ToolVisibility,
		RemoteSearch:      pending.RemoteSearch,
	}

	var saveErr error
	if pending.Reauth {
		// Update existing server — preserve fields not touched by OAuth
		existing, err := a.mcpStorage.Get(r.Context(), pending.Namespace)
		if err != nil {
			http.Error(w, "server not found: "+err.Error(), http.StatusNotFound)
			return
		}
		existing.AuthType = "oauth2"
		existing.OAuthClientID = pending.ClientID
		existing.OAuthTokenURL = pending.TokenURL
		existing.OAuthAccessToken = tokens.AccessToken
		existing.OAuthRefreshToken = tokens.RefreshToken
		existing.Token = ""
		saveErr = a.mcpStorage.Update(r.Context(), existing)
	} else {
		saveErr = a.mcpStorage.Create(r.Context(), server)
	}

	if saveErr != nil {
		http.Error(w, "failed to save server: "+saveErr.Error(), http.StatusInternalServerError)
		return
	}

	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	// Restore the admin session cookie so the user doesn't have to log in again
	if pending.SessionToken != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_session",
			Value:    pending.SessionToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	http.Redirect(w, r, "/admin/mcp-servers", http.StatusFound)
}

func scheme(r *http.Request) string {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}
