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

// Role identifies the access scope of an authenticated session. Admin can
// reach the management UI and chat; Chat can reach only the chat UI (and the
// read-only MCP listing APIs the chat UI needs).
type Role string

const (
	RoleAdmin Role = "admin"
	RoleChat  Role = "chat"
)

// Session represents an authenticated admin or chat session.
type Session struct {
	Token     string
	Role      Role
	CreatedAt time.Time
}

// Admin handles admin UI requests
type Admin struct {
	password     string // admin password ("")
	chatPassword string // chat-only password ("" = no chat-only access)
	sessions     map[string]*Session
	sessionsMu   sync.RWMutex
	templates    *TemplateRenderer
	getStats     func() *Stats
	getProviders func() []ProviderInfo
	getModels    func() []ModelInfo

	// MCP callbacks (for read-only display of config-based servers)
	getMCPServers   func() []MCPServerInfo
	getMCPTools     func(namespace string) ([]ToolInfo, error)
	getMCPResources func(namespace string) ([]ResourceInfo, error)
	getMCPPrompts   func(namespace string) ([]PromptInfo, error)

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
	RemoteSearch      bool     `json:"remote_search"`
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
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Arguments   []PromptArgument  `json:"arguments,omitempty"`
}

// PromptArgument describes one argument a prompt accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// New creates a new Admin handler
func New(config *types.Config, getStats func() *Stats, getProviders func() []ProviderInfo, getMCPServers func() []MCPServerInfo, getMCPTools func(string) ([]ToolInfo, error), getMCPResources func(string) ([]ResourceInfo, error), getMCPPrompts func(string) ([]PromptInfo, error), getModels func() []ModelInfo, mcpStorage storage.MCPStorage, mcpStorageWritable bool, onMCPServerChange func(), onMCPCacheRefresh func()) *Admin {
	if config.Server.AdminPassword == "" {
		return nil
	}

	return &Admin{
		password:           config.Server.AdminPassword,
		chatPassword:       config.Server.ChatPassword,
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

// ChatPageHandler returns the HTTP handler for /chat. It wraps the page
// renderer with ChatAuthMiddleware (nil when no chat_password is set,
// meaning open chat). The handler renders web/templates/chat.html — the
// host's own copy, not webchat's example — so Tailwind's source scan
// during `npm run build` picks up every utility class used in the
// template and emits them into dist/assets/main.css. This is the fix for
// the long-running "icons blow up, layouts break" class of bug.
func (a *Admin) ChatPageHandler() http.HandlerFunc {
	render := func(w http.ResponseWriter, r *http.Request) {
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
	}
	mw := a.ChatAuthMiddleware()
	if mw == nil {
		return render
	}
	return func(w http.ResponseWriter, r *http.Request) {
		mw(http.HandlerFunc(render)).ServeHTTP(w, r)
	}
}

// Enabled returns true if admin UI is enabled (admin password set).
func (a *Admin) Enabled() bool {
	return a != nil && a.password != ""
}

// ChatAuthMiddleware returns the auth middleware to wrap webchat routes, or
// nil if no auth is required. Chat is gated ONLY by chat_password: if no
// chat_password is configured the chat is open (even when admin_password is
// set, since "logged-in admin" + "no chat password" should still let the
// admin walk straight into chat without re-authenticating). When chat_password
// is set, both admin and chat sessions are accepted.
func (a *Admin) ChatAuthMiddleware() func(http.Handler) http.Handler {
	if a == nil || a.chatPassword == "" {
		return nil
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := a.getSessionFromRequest(r)
			if token == "" {
				token = a.getSessionFromCookie(r)
			}
			if a.sessionRole(token) == "" {
				// API requests get JSON 401; page requests redirect to the
				// shared admin login (it accepts both passwords and routes by
				// role on success).
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

// generateToken generates a random session token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// createSession creates a new session with the given role and returns the token.
func (a *Admin) createSession(role Role) (string, error) {
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
		Role:      role,
		CreatedAt: now,
	}

	return token, nil
}

// validateSession checks if a session token is valid (any role).
func (a *Admin) validateSession(token string) bool {
	return a.sessionRole(token) != ""
}

// sessionRole returns the role attached to the session token, or "" if invalid.
func (a *Admin) sessionRole(token string) Role {
	if token == "" {
		return ""
	}

	a.sessionsMu.RLock()
	defer a.sessionsMu.RUnlock()

	sess, ok := a.sessions[token]
	if !ok {
		return ""
	}

	// Check if session is expired (24 hours)
	if time.Since(sess.CreatedAt) > 24*time.Hour {
		return ""
	}

	return sess.Role
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

// requireAuth is middleware that checks for an authenticated ADMIN session (for
// admin API endpoints). Chat-only sessions are rejected with 403 so a logged-in
// chat user can't poke at management APIs.
func (a *Admin) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := a.getSessionFromRequest(r)
		if token == "" {
			token = a.getSessionFromCookie(r)
		}
		if a.sessionRole(token) != RoleAdmin {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// requirePageAuth is middleware that checks for an authenticated ADMIN page
// request. Redirects to the admin login page if unauthenticated.
func (a *Admin) requirePageAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := a.getSessionFromCookie(r)
		if a.sessionRole(token) != RoleAdmin {
			http.Redirect(w, r, "/admin/login?return="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireReadOnlyPageAuth is middleware for admin pages a chat user may also
// view (e.g. MCP servers listing). Both admin and chat sessions pass;
// unauthenticated requests redirect to /admin/login so the user gets a chance
// to authenticate. The handler is expected to consult sessionRole to decide
// what UI affordances to render.
func (a *Admin) requireReadOnlyPageAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := a.getSessionFromCookie(r)
		if a.sessionRole(token) == "" {
			http.Redirect(w, r, "/admin/login?return="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}

// sessionRoleFromRequest returns the role attached to the request's session
// (admin or chat), or "" if unauthenticated. Handlers use this to render
// read-only vs editable UI.
func (a *Admin) sessionRoleFromRequest(r *http.Request) Role {
	token := a.getSessionFromRequest(r)
	if token == "" {
		token = a.getSessionFromCookie(r)
	}
	return a.sessionRole(token)
}
