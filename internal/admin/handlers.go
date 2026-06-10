package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/paularlott/llmrouter/internal/storage"
)

// RegisterRoutes registers admin routes on the given mux
func (a *Admin) RegisterRoutes(mux *http.ServeMux) {
	if !a.Enabled() {
		return
	}

	// Static assets
	mux.HandleFunc("/admin/assets/", ServeStatic)

	// Pages - use requirePageAuth which redirects to login
	mux.HandleFunc("/admin/login", a.HandleLoginPage)
	mux.HandleFunc("/admin/", a.requirePageAuth(a.HandleDashboard))
	mux.HandleFunc("/admin/mcp-servers", a.requirePageAuth(a.HandleMCPServersPage))
	mux.HandleFunc("/admin/models", a.requirePageAuth(a.HandleModelsPage))

	// API endpoints - use requireAuth which returns 401
	mux.HandleFunc("POST /admin/api/login", a.HandleLogin)
	mux.HandleFunc("POST /admin/api/logout", a.HandleLogout)
	mux.HandleFunc("GET /admin/api/stats", a.requireAuth(a.HandleStats))
	mux.HandleFunc("GET /admin/api/providers", a.requireAuth(a.HandleProviders))
	mux.HandleFunc("GET /admin/api/models", a.requireAuth(a.HandleModels))
	mux.HandleFunc("GET /admin/api/mcp-servers", a.requireAuth(a.HandleListMCPServers))
	mux.HandleFunc("POST /admin/api/mcp-servers", a.requireAuth(a.HandleCreateMCPServer))
	mux.HandleFunc("GET /admin/api/mcp-servers/{namespace}", a.requireAuth(a.HandleGetMCPServer))
	mux.HandleFunc("PUT /admin/api/mcp-servers/{namespace}", a.requireAuth(a.HandleUpdateMCPServer))
	mux.HandleFunc("PUT /admin/api/mcp-servers/{namespace}/toggle", a.requireAuth(a.HandleToggleMCPServer))
	mux.HandleFunc("DELETE /admin/api/mcp-servers/{namespace}", a.requireAuth(a.HandleDeleteMCPServer))
	mux.HandleFunc("GET /admin/api/mcp-servers/{namespace}/tools", a.requireAuth(a.HandleGetMCPServerTools))
	mux.HandleFunc("PUT /admin/api/mcp-servers/{namespace}/tools/toggle", a.requireAuth(a.HandleToggleMCPServerTool))
	mux.HandleFunc("POST /admin/api/mcp-servers/refresh-cache", a.requireAuth(a.HandleRefreshMCPCache))
	mux.HandleFunc("GET /admin/api/mcp-storage-status", a.requireAuth(a.HandleMCPStorageStatus))

	// OAuth2 PKCE flow for MCP servers
	mux.HandleFunc("POST /admin/api/mcp-servers/oauth/start", a.requireAuth(a.HandleOAuthStart))
	mux.HandleFunc("GET /admin/oauth/callback", a.HandleOAuthCallback)
}

// HandleLoginPage renders the login page
func (a *Admin) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in via cookie, redirect to dashboard or return URL
	token := a.getSessionFromCookie(r)
	if a.validateSession(token) {
		returnURL := r.URL.Query().Get("return")
		if returnURL == "" {
			returnURL = "/admin/"
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
		return
	}

	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "login.html", data)
}

// HandleDashboard renders the dashboard page
func (a *Admin) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "dashboard.html", data)
}

// Serve404 renders the 404 page
func (a *Admin) Serve404(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "404.html", data)
}

// HandleMCPServersPage renders the MCP servers page
func (a *Admin) HandleMCPServersPage(w http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "mcp-servers.html", data)
}

// HandleLogin handles login requests
func (a *Admin) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Parse password based on content type
	var password string
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var creds struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&creds); err == nil {
			password = creds.Password
		}
	} else {
		r.ParseForm()
		password = r.FormValue("password")
	}

	if password == "" {
		writeError(w, http.StatusBadRequest, "password required")
		return
	}

	if password != a.password {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	token, err := a.createSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	// Check if this is an API request (expects JSON) or form submission
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	} else {
		// Form submission - redirect to return URL or dashboard
		returnURL := r.URL.Query().Get("return")
		if returnURL == "" {
			returnURL = "/admin/"
		}
		http.Redirect(w, r, returnURL, http.StatusFound)
	}
}

// HandleLogout invalidates the session
func (a *Admin) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token := a.getSessionFromCookie(r)
	if token != "" {
		a.deleteSession(token)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusOK)
}

// HandleStats returns dashboard statistics
func (a *Admin) HandleStats(w http.ResponseWriter, r *http.Request) {
	if a.getStats == nil {
		writeJSON(w, http.StatusOK, &Stats{})
		return
	}
	writeJSON(w, http.StatusOK, a.getStats())
}


// HandleProviders returns provider information
func (a *Admin) HandleProviders(w http.ResponseWriter, r *http.Request) {
	if a.getProviders == nil {
		writeJSON(w, http.StatusOK, []ProviderInfo{})
		return
	}
	writeJSON(w, http.StatusOK, a.getProviders())
}
// HandleModelsPage renders the models page
func (a *Admin) HandleModelsPage(w http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "models.html", data)
}

// HandleModels returns model information
func (a *Admin) HandleModels(w http.ResponseWriter, r *http.Request) {
	if a.getModels == nil {
		writeJSON(w, http.StatusOK, []ModelInfo{})
		return
	}
	writeJSON(w, http.StatusOK, a.getModels())
}

func (a *Admin) HandleListMCPServers(w http.ResponseWriter, r *http.Request) {
	// getMCPServers already returns both static and dynamic servers
	if a.getMCPServers == nil {
		writeJSON(w, http.StatusOK, []MCPServerInfo{})
		return
	}
	writeJSON(w, http.StatusOK, a.getMCPServers())
}

// HandleMCPStorageStatus returns whether MCP server storage is writable
func (a *Admin) HandleMCPStorageStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"writable": a.mcpStorageWritable,
	})
}

// HandleGetMCPServer returns a single MCP server
func (a *Admin) HandleGetMCPServer(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	// First check dynamic servers in storage
	if a.mcpStorage != nil {
		server, err := a.mcpStorage.Get(r.Context(), namespace)
		if err == nil {
		writeJSON(w, http.StatusOK, MCPServerInfo{
			Namespace:      server.Namespace,
			URL:            server.URL,
			AuthType:       server.AuthType,
			Enabled:        server.Enabled,
			ToolVisibility: server.ToolVisibility,
			ToolAllowlist:  server.ToolAllowlist,
			ToolDenylist:   server.ToolDenylist,
			StaticServer:   false,
			RemoteSearch:   server.RemoteSearch,
		})
		return
	}
	}

	// Then check static servers from config
	if a.getMCPServers != nil {
		for _, s := range a.getMCPServers() {
			if s.Namespace == namespace {
				writeJSON(w, http.StatusOK, s)
				return
			}
		}
	}

	writeError(w, http.StatusNotFound, "server not found")
}

// HandleCreateMCPServer creates a new MCP server
func (a *Admin) HandleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	if a.mcpStorage == nil || !a.mcpStorageWritable {
		writeError(w, http.StatusBadRequest, "MCP server storage requires a configured storage path")
		return
	}

	var req struct {
		Namespace         string   `json:"namespace"`
		URL               string   `json:"url"`
		AuthType          string   `json:"auth_type"`
		Token             string   `json:"token"`
		OAuthTokenURL     string   `json:"oauth_token_url"`
		OAuthAccessToken  string   `json:"oauth_access_token"`
		OAuthRefreshToken string   `json:"oauth_refresh_token"`
		Enabled           bool     `json:"enabled"`
		ToolVisibility    string   `json:"tool_visibility"`
		ToolAllowlist     []string `json:"tool_allowlist"`
		ToolDenylist      []string `json:"tool_denylist"`
		RemoteSearch      bool     `json:"remote_search"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace is required")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	if req.ToolVisibility == "" {
		req.ToolVisibility = "native"
	}

	server := &storage.MCPServerConfig{
		Namespace:         req.Namespace,
		URL:               strings.TrimSuffix(req.URL, "/"),
		AuthType:          req.AuthType,
		Token:             req.Token,
		OAuthTokenURL:     req.OAuthTokenURL,
		OAuthAccessToken:  req.OAuthAccessToken,
		OAuthRefreshToken: req.OAuthRefreshToken,
		Enabled:           req.Enabled,
		ToolVisibility:    req.ToolVisibility,
		ToolAllowlist:     req.ToolAllowlist,
		ToolDenylist:      req.ToolDenylist,
		RemoteSearch:      req.RemoteSearch,
	}

	if err := a.mcpStorage.Create(r.Context(), server); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	writeJSON(w, http.StatusCreated, MCPServerInfo{
		Namespace:      server.Namespace,
		URL:            server.URL,
		AuthType:       server.AuthType,
		Enabled:        server.Enabled,
		ToolVisibility: server.ToolVisibility,
		ToolAllowlist:  server.ToolAllowlist,
		ToolDenylist:   server.ToolDenylist,
		StaticServer:   false,
		RemoteSearch:   server.RemoteSearch,
	})
}

// HandleUpdateMCPServer updates an MCP server
func (a *Admin) HandleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	if a.mcpStorage == nil || !a.mcpStorageWritable {
		writeError(w, http.StatusBadRequest, "MCP server storage requires a configured storage path")
		return
	}

	// Get existing server
	server, err := a.mcpStorage.Get(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found or is a static server")
		return
	}

	var req struct {
		URL               string   `json:"url"`
		AuthType          string   `json:"auth_type"`
		Token             string   `json:"token"`
		OAuthTokenURL     string   `json:"oauth_token_url"`
		OAuthAccessToken  string   `json:"oauth_access_token"`
		OAuthRefreshToken string   `json:"oauth_refresh_token"`
		Enabled           bool     `json:"enabled"`
		ToolVisibility    string   `json:"tool_visibility"`
		ToolAllowlist     []string `json:"tool_allowlist"`
		ToolDenylist      []string `json:"tool_denylist"`
		RemoteSearch      bool     `json:"remote_search"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL != "" {
		server.URL = strings.TrimSuffix(req.URL, "/")
	}
	server.AuthType = req.AuthType
	if req.AuthType == "oauth2" {
		server.OAuthTokenURL = req.OAuthTokenURL
		if req.OAuthAccessToken != "" {
			server.OAuthAccessToken = req.OAuthAccessToken
		}
		if req.OAuthRefreshToken != "" {
			server.OAuthRefreshToken = req.OAuthRefreshToken
		}
		server.Token = ""
	} else {
		if req.Token != "" {
			server.Token = req.Token
		}
		server.OAuthTokenURL = ""
		server.OAuthAccessToken = ""
		server.OAuthRefreshToken = ""
	}
	server.Enabled = req.Enabled
	if req.ToolVisibility != "" {
		server.ToolVisibility = req.ToolVisibility
	}
	server.ToolAllowlist = req.ToolAllowlist
	server.ToolDenylist = req.ToolDenylist
	server.RemoteSearch = req.RemoteSearch

	if err := a.mcpStorage.Update(r.Context(), server); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update server")
		return
	}

	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	writeJSON(w, http.StatusOK, MCPServerInfo{
		Namespace:      server.Namespace,
		URL:            server.URL,
		AuthType:       server.AuthType,
		Enabled:        server.Enabled,
		ToolVisibility: server.ToolVisibility,
		ToolAllowlist:  server.ToolAllowlist,
		ToolDenylist:   server.ToolDenylist,
		StaticServer:   false,
		RemoteSearch:   server.RemoteSearch,
	})
}

// HandleDeleteMCPServer deletes an MCP server
func (a *Admin) HandleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	if a.mcpStorage == nil || !a.mcpStorageWritable {
		writeError(w, http.StatusBadRequest, "MCP server storage requires a configured storage path")
		return
	}

	if err := a.mcpStorage.Delete(r.Context(), namespace); err != nil {
		writeError(w, http.StatusNotFound, "server not found or is a static server")
		return
	}

	// Notify router to reload MCP servers
	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleGetMCPServerTools returns tools for an MCP server
func (a *Admin) HandleGetMCPServerTools(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	if a.getMCPTools == nil {
		writeJSON(w, http.StatusOK, []ToolInfo{})
		return
	}

	tools, err := a.getMCPTools(namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// If we have storage, check disabled tools
	if a.mcpStorage != nil {
		server, err := a.mcpStorage.Get(r.Context(), namespace)
		if err == nil {
			// Build a set of disabled tools for quick lookup
			disabledSet := make(map[string]bool)
			for _, t := range server.DisabledTools {
				disabledSet[t] = true
			}

			// Update enabled status
			for i := range tools {
				tools[i].Enabled = !disabledSet[tools[i].Name]
			}
		}
	}

	writeJSON(w, http.StatusOK, tools)
}

// HandleToggleMCPServerTool toggles a tool's enabled state (lazy write)
func (a *Admin) HandleToggleMCPServerTool(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	if a.mcpStorage == nil {
		writeError(w, http.StatusInternalServerError, "storage not available")
		return
	}

	var req struct {
		ToolName string `json:"tool_name"`
		Enabled  bool   `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "tool_name is required")
		return
	}

	if err := a.mcpStorage.ToggleTool(r.Context(), namespace, req.ToolName, req.Enabled); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Notify router to reload MCP servers (lazy - only when tool toggles happen)
	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// HandleToggleMCPServer toggles an MCP server's enabled state
func (a *Admin) HandleToggleMCPServer(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	if a.mcpStorage == nil {
		writeError(w, http.StatusInternalServerError, "storage not available")
		return
	}

	// Get existing server
	server, err := a.mcpStorage.Get(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusNotFound, "server not found or is a static server")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update enabled state
	server.Enabled = req.Enabled

	if err := a.mcpStorage.Update(r.Context(), server); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update server")
		return
	}

	if a.onMCPServerChange != nil {
		a.onMCPServerChange()
	}

	writeJSON(w, http.StatusOK, MCPServerInfo{
		Namespace:      server.Namespace,
		URL:            server.URL,
		AuthType:       server.AuthType,
		Enabled:        server.Enabled,
		ToolVisibility: server.ToolVisibility,
		ToolAllowlist:  server.ToolAllowlist,
		ToolDenylist:   server.ToolDenylist,
		StaticServer:   false,
		RemoteSearch:   server.RemoteSearch,
	})
}

// HandleRefreshMCPCache refreshes the tool cache from all remote MCP servers
func (a *Admin) HandleRefreshMCPCache(w http.ResponseWriter, r *http.Request) {
	if a.onMCPCacheRefresh == nil {
		writeError(w, http.StatusInternalServerError, "cache refresh not available")
		return
	}

	a.onMCPCacheRefresh()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
