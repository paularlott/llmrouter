package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/web"
)

// Session represents an admin session
type Session struct {
	Token     string
	CreatedAt time.Time
}

// Admin handles admin UI requests
type Admin struct {
	password     string
	sessions     map[string]*Session
	sessionsMu   sync.RWMutex
	templates    *TemplateRenderer
	getStats     func() *Stats
	getProviders func() []ProviderInfo
	getModels    func() []ModelInfo

	// MCP callbacks (for read-only display of config-based servers)
	getMCPServers func() []MCPServerInfo
	getMCPTools   func(namespace string) ([]ToolInfo, error)

	// MCP storage (for dynamic server management)
	mcpStorage         storage.MCPStorage
	mcpStorageWritable bool // true if storage is persistent (not memory-only)
	onMCPServerChange  func()
	onMCPCacheRefresh  func() // Called to refresh tool cache from remote servers
}

// Stats represents dashboard statistics
type Stats struct {
	Providers       int `json:"providers"`
	Models          int `json:"models"`
	MCPServers      int `json:"mcp_servers"`
	ActiveRequests  int `json:"active_requests"`
}

// ProviderInfo represents provider information for the UI
type ProviderInfo struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Healthy     bool    `json:"healthy"`
	ModelCount  int     `json:"model_count"`
	Weight      float64 `json:"weight"`
}

// MCPServerInfo represents MCP server information for the UI
type MCPServerInfo struct {
	Namespace         string   `json:"namespace"`
	URL               string   `json:"url"`
	AuthType          string   `json:"auth_type,omitempty"`
	Enabled           bool     `json:"enabled"`
	ToolVisibility    string   `json:"tool_visibility"`
	ToolAllowlist     []string `json:"tool_allowlist,omitempty"`
	ToolDenylist      []string `json:"tool_denylist,omitempty"`
	StaticServer      bool     `json:"static_server"`
}

// ModelInfo represents a model and its providers for the UI
type ModelInfo struct {
	ID        string   `json:"id"`
	Providers []string `json:"providers"`
}

// ToolInfo represents tool information for the UI
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	Enabled     bool                   `json:"enabled"`
}

// New creates a new Admin handler
func New(config *types.Config, getStats func() *Stats, getProviders func() []ProviderInfo, getMCPServers func() []MCPServerInfo, getMCPTools func(string) ([]ToolInfo, error), getModels func() []ModelInfo, mcpStorage storage.MCPStorage, mcpStorageWritable bool, onMCPServerChange func(), onMCPCacheRefresh func()) *Admin {
	if config.Server.AdminPassword == "" {
		return nil
	}

	return &Admin{
		password:           config.Server.AdminPassword,
		sessions:           make(map[string]*Session),
		templates:          NewTemplateRenderer(web.Templates),
		getStats:           getStats,
		getProviders:       getProviders,
		getMCPServers:      getMCPServers,
		getMCPTools:        getMCPTools,
		getModels:          getModels,
		mcpStorage:         mcpStorage,
		mcpStorageWritable: mcpStorageWritable,
		onMCPServerChange:  onMCPServerChange,
		onMCPCacheRefresh:  onMCPCacheRefresh,
	}
}

// Enabled returns true if admin UI is enabled
func (a *Admin) Enabled() bool {
	return a != nil && a.password != ""
}

// generateToken generates a random session token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// createSession creates a new session and returns the token
func (a *Admin) createSession() (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()

	// Clean up old sessions (older than 24 hours)
	now := time.Now()
	for tok, sess := range a.sessions {
		if now.Sub(sess.CreatedAt) > 24*time.Hour {
			delete(a.sessions, tok)
		}
	}

	a.sessions[token] = &Session{
		Token:     token,
		CreatedAt: now,
	}

	return token, nil
}

// validateSession checks if a session token is valid
func (a *Admin) validateSession(token string) bool {
	if token == "" {
		return false
	}

	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()

	sess, ok := a.sessions[token]
	if !ok {
		return false
	}

	// Check if session is expired (24 hours)
	if time.Since(sess.CreatedAt) > 24*time.Hour {
		return false
	}

	return true
}

// deleteSession removes a session
func (a *Admin) deleteSession(token string) {
	a.sessionsMu.Lock()
	defer a.sessionsMu.Unlock()
	delete(a.sessions, token)
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes an error response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// getSessionFromRequest extracts the session token from a request
func (a *Admin) getSessionFromRequest(r *http.Request) string {
	// Check Authorization header first
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:]
	}
	return ""
}

// getSessionFromCookie extracts the session token from a cookie
func (a *Admin) getSessionFromCookie(r *http.Request) string {
	cookie, err := r.Cookie("admin_session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// requireAuth is middleware that checks for valid authentication (for API endpoints)
func (a *Admin) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := a.getSessionFromRequest(r)
		if token == "" {
			token = a.getSessionFromCookie(r)
		}
		if !a.validateSession(token) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// requirePageAuth is middleware that checks for valid authentication (for page requests)
// Redirects to login page if not authenticated
func (a *Admin) requirePageAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check cookie for page requests
		token := a.getSessionFromCookie(r)
		if !a.validateSession(token) {
			// Redirect to login page with return URL
			returnURL := r.URL.Path
			http.Redirect(w, r, "/admin/login?return="+returnURL, http.StatusFound)
			return
		}
		next(w, r)
	}
}
