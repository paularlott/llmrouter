package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/web"
)

// Session represents an authenticated session.
type Session struct {
	Token     string
	CreatedAt time.Time
}

// Admin handles admin UI requests
type Admin struct {
	password      string
	sessions      map[string]*Session
	sessionsMu    sync.RWMutex
	templates     *TemplateRenderer
	getStats      func() *Stats
	getProviders  func() []ProviderInfo
	getModels     func() []ModelInfo
	refreshModels func() // Called to force a rescan of models from all providers

	// MCP callbacks (for read-only display of config-based servers)
	getMCPServers   func() []MCPServerInfo
	getMCPTools     func(namespace string) ([]ToolInfo, error)
	getMCPResources func(namespace string) ([]ResourceInfo, error)
	getMCPPrompts   func(namespace string) ([]PromptInfo, error)
	callMCPTool     func(namespace, toolName string, args map[string]any) (*ToolCallResult, error)

	// MCP storage (for dynamic server management)
	mcpStorage         storage.MCPStorage
	mcpStorageWritable bool // true if storage is persistent (not memory-only)
	onMCPServerChange  func()
	onMCPCacheRefresh  func() // Called to refresh tool cache from remote servers

	// Provider storage (for dynamic provider management via UI)
	providerStorage         storage.ProviderStorage
	providerStorageWritable bool
	onProviderChange        func()

	// Persona storage (for dynamic persona management via UI)
	personaStorage         storage.PersonaStorage
	personaStorageWritable bool
	onPersonaChange        func()
	getPersonas            func() []PersonaInfo

	// Request watcher (for live LLM data flow viewer on providers page)
	watcherSubscribe   func() chan []byte // returns a channel of JSON-encoded events
	watcherUnsubscribe func(chan []byte)  // closes and removes the channel
}

// Stats represents dashboard statistics
type Stats struct {
	Providers      int `json:"providers"`
	Models         int `json:"models"`
	MCPServers     int `json:"mcp_servers"`
	ActiveRequests int `json:"active_requests"`
}

// ProviderInfo represents provider information for the UI
type ProviderInfo struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Healthy        bool    `json:"healthy"`
	ModelCount     int     `json:"model_count"`
	Weight         float64 `json:"weight"`
	StaticProvider bool    `json:"static_provider"` // true = from config file, false = from UI/storage
}

// MCPServerInfo represents MCP server information for the UI
type MCPServerInfo struct {
	Namespace      string   `json:"namespace"`
	URL            string   `json:"url"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	Env            []string `json:"env,omitempty"`
	AuthType       string   `json:"auth_type,omitempty"`
	Enabled        bool     `json:"enabled"`
	ToolVisibility string   `json:"tool_visibility"`
	ToolAllowlist  []string `json:"tool_allowlist,omitempty"`
	ToolDenylist   []string `json:"tool_denylist,omitempty"`
	StaticServer   bool     `json:"static_server"`
	RemoteSearch   bool     `json:"remote_search"`
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

// ToolCallResult represents the result of executing a tool via the admin UI.
// It mirrors the relevant parts of mcp.ToolResponse without coupling this
// package to the mcp dependency.
type ToolCallResult struct {
	Content           []ToolCallContent `json:"content"`
	StructuredContent interface{}       `json:"structuredContent,omitempty"`
}

// ToolCallContent is one content item in a tool call result.
type ToolCallContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ResourceInfo represents a resource (static or template) exposed by an MCP
// server. Templates carry a URITemplate with {var} placeholders; static
// resources carry a concrete URI. Both are shown read-only in the UI — the
// router has no per-resource enable/disable toggle the way it does for tools.
type ResourceInfo struct {
	URI         string `json:"uri"`
	Template    bool   `json:"template"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mime_type,omitempty"`
}

// PromptInfo represents a prompt exposed by an MCP server. Arguments is the
// (possibly empty) list of named arguments the prompt accepts.
type PromptInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes one argument a prompt accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// PersonaInfo represents a persona for the management UI. Static=true means
// it came from a .toml file in PersonasDir (read-only); Static=false means it
// lives in KV storage and supports full CRUD.
type PersonaInfo struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	DefaultModel string                 `json:"default_model,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"`
	Static       bool                   `json:"static"`
}

// New creates a new Admin handler
func New(config *types.Config, getStats func() *Stats, getProviders func() []ProviderInfo, getMCPServers func() []MCPServerInfo, getMCPTools func(string) ([]ToolInfo, error), getMCPResources func(string) ([]ResourceInfo, error), getMCPPrompts func(string) ([]PromptInfo, error), getModels func() []ModelInfo, mcpStorage storage.MCPStorage, mcpStorageWritable bool, onMCPServerChange func(), onMCPCacheRefresh func()) *Admin {
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
		getMCPResources:    getMCPResources,
		getMCPPrompts:      getMCPPrompts,
		getModels:          getModels,
		mcpStorage:         mcpStorage,
		mcpStorageWritable: mcpStorageWritable,
		onMCPServerChange:  onMCPServerChange,
		onMCPCacheRefresh:  onMCPCacheRefresh,
	}
}

// SetProviderStorage wires dynamic provider management. Called after New
// but before RegisterRoutes. If ps is nil, provider management is disabled
// (config-file providers are still shown read-only).
func (a *Admin) SetProviderStorage(ps storage.ProviderStorage, writable bool, onChange func()) {
	a.providerStorage = ps
	a.providerStorageWritable = writable
	a.onProviderChange = onChange
}

// SetPersonaStorage wires dynamic persona management. Called after New but
// before RegisterRoutes. getPersonas returns the merged file+stored list for
// display; CRUD handlers operate on ps directly. If ps is nil, persona
// management is disabled (config-file personas are still shown read-only).
func (a *Admin) SetPersonaStorage(ps storage.PersonaStorage, writable bool, onChange func(), getPersonas func() []PersonaInfo) {
	a.personaStorage = ps
	a.personaStorageWritable = writable
	a.onPersonaChange = onChange
	a.getPersonas = getPersonas
}

// SetMCPToolCaller wires live tool execution from the admin UI. Called after
// New but before RegisterRoutes. If fn is nil, the tool-call endpoint returns
// 503 (tool execution not available).
func (a *Admin) SetMCPToolCaller(fn func(namespace, toolName string, args map[string]any) (*ToolCallResult, error)) {
	a.callMCPTool = fn
}

// SetRefreshModels wires a forced model rescan from the admin UI. Called
// after New but before RegisterRoutes. If fn is nil, the rescan endpoint
// returns 500 (refresh not available).
func (a *Admin) SetRefreshModels(fn func()) {
	a.refreshModels = fn
}

// SetRequestWatcher wires the live LLM data flow viewer. subscribe returns a
// channel that delivers JSON-encoded WatchEvent payloads. unsubscribe closes
// and removes the channel. When both are nil, the watch endpoint returns 503.
func (a *Admin) SetRequestWatcher(subscribe func() chan []byte, unsubscribe func(chan []byte)) {
	a.watcherSubscribe = subscribe
	a.watcherUnsubscribe = unsubscribe
}

// ChatPageHandler returns the HTTP handler for /chat. Uses requirePageAuth
// (same admin session as every other page). One password, one role.
func (a *Admin) ChatPageHandler() http.HandlerFunc {
	return a.requirePageAuth(func(w http.ResponseWriter, r *http.Request) {
		data := &TemplateData{
			CSSFile: "/admin/assets/main.css",
			JSFile:  "/admin/assets/main.js",
			Prefix:  "/chat",
		}
		if a.templates == nil {
			http.Error(w, "templates not configured", http.StatusInternalServerError)
			return
		}
		if err := a.templates.Render(w, "chat.html", data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	})
}

// Enabled returns true if admin UI is enabled (admin password set).
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

// createSession creates a new session and returns the token.
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

// validateSession checks if a session token is valid.
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
	return time.Since(sess.CreatedAt) <= 24*time.Hour
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

// RequireAuthMiddleware returns an http.Handler middleware that checks for
// a valid session. Used by lmchatkit to gate its API + asset routes with the
// same admin session as the rest of the app. Returns nil if no admin
// password is set (open access — for local dev behind a reverse proxy).
func (a *Admin) RequireAuthMiddleware() func(http.Handler) http.Handler {
	if a == nil || a.password == "" {
		return nil
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := a.getSessionFromRequest(r)
			if token == "" {
				token = a.getSessionFromCookie(r)
			}
			if !a.validateSession(token) {
				if strings.HasPrefix(r.URL.Path, "/chat/api/") {
					writeError(w, http.StatusUnauthorized, "unauthorized")
					return
				}
				http.Redirect(w, r, "/admin/login?return="+r.URL.Path, http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAuth is middleware that checks for a valid session (API endpoints).
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

// requirePageAuth is middleware that checks for a valid session (page requests).
// Redirects to login if unauthenticated.
func (a *Admin) requirePageAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := a.getSessionFromCookie(r)
		if !a.validateSession(token) {
			http.Redirect(w, r, "/admin/login?return="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}
