package admin

import (
	"encoding/json"
	"net/http"
	"strings"
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
	mux.HandleFunc("DELETE /admin/api/mcp-servers/{namespace}", a.requireAuth(a.HandleDeleteMCPServer))
	mux.HandleFunc("GET /admin/api/mcp-servers/{namespace}/tools", a.requireAuth(a.HandleGetMCPServerTools))
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
	if a.getMCPServers == nil {
		writeJSON(w, http.StatusOK, []MCPServerInfo{})
		return
	}
	writeJSON(w, http.StatusOK, a.getMCPServers())
}

// HandleGetMCPServer returns a single MCP server
func (a *Admin) HandleGetMCPServer(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	servers := a.getMCPServers()
	for _, s := range servers {
		if s.Namespace == namespace {
			writeJSON(w, http.StatusOK, s)
			return
		}
	}

	writeError(w, http.StatusNotFound, "server not found")
}

// HandleCreateMCPServer creates a new MCP server
func (a *Admin) HandleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	// Note: This is a placeholder - actual creation is handled by the router
	// through storage persistence
	writeError(w, http.StatusNotImplemented, "MCP server creation via API not yet implemented")
}

// HandleUpdateMCPServer updates an MCP server
func (a *Admin) HandleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	// Note: This is a placeholder - actual update is handled by the router
	// through storage persistence
	writeError(w, http.StatusNotImplemented, "MCP server update via API not yet implemented")
}

// HandleDeleteMCPServer deletes an MCP server
func (a *Admin) HandleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	// Note: This is a placeholder - actual deletion is handled by the router
	// through storage persistence
	writeError(w, http.StatusNotImplemented, "MCP server deletion via API not yet implemented")
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

	writeJSON(w, http.StatusOK, tools)
}
