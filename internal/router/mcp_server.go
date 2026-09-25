package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
	"slices"
)

// remoteServerClient holds a client and its config for admin UI tool listing
type remoteServerClient struct {
	client      *mcp.Client
	config      types.MCPRemoteServerConfig
	enabled     bool // storage-based servers can be disabled; disabled ones are never contacted for listings
	initialized bool
}

// ensureInitialized lazily initializes the client if needed
func (r *remoteServerClient) ensureInitialized(ctx context.Context) error {
	if r.initialized {
		return nil
	}
	if err := r.client.Initialize(ctx); err != nil {
		return err
	}
	r.initialized = true
	return nil
}

// declareUIAppsSupport advertises this client's support for the MCP Apps
// extension (SEP-1865) to a remote server, mirroring what this router's own
// chat UI (lmchatkit's mountAppView) actually renders: a sandboxed iframe
// for a tool linked to a ui:// resource, federated the same as a native
// tool. Without this, a spec-conformant remote server that only attaches
// _meta.ui for clients that declared capabilities.extensions[io.model
// contextprotocol/ui] has no way to know this router can render one, and
// silently serves a plain-text-only tool instead — MCP Apps then quietly
// never works for that server, with no error anywhere to explain why.
func declareUIAppsSupport(client *mcp.Client) {
	client.DeclareExtension(mcp.UIAppsExtensionID, map[string]any{
		"mimeTypes": []string{mcp.UIAppMimeType},
	})
}

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server               *mcp.Server
	endpointServer       *mcp.Server // the public /mcp endpoint: llmrouter's own tools plus opt-in federated servers (never their apps)
	config               *types.Config
	logger               Logger
	remoteClients        map[string]*remoteServerClient // namespace -> client (for admin UI)
	pendingStaticEntries []mcp.RemoteServerEntry
	scriptlingManager    *scriptlingToolManager // Scriptling-based tool manager (if configured)
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(config *types.Config, logger Logger) (*MCPServer, error) {
	server := mcp.NewServer("llmrouter", "1.0.0")

	mcpServer := &MCPServer{
		server:         server,
		endpointServer: mcp.NewServer("llmrouter", "1.0.0"),
		config:         config,
		logger:         logger,
		remoteClients:  make(map[string]*remoteServerClient),
	}

	// Register static servers from config
	entries := make([]mcp.RemoteServerEntry, 0, len(config.MCP.RemoteServers))

	for _, remoteServer := range config.MCP.RemoteServers {
		entry, rsClient := mcpServer.createRemoteServerEntry(remoteServer, nil)
		entries = append(entries, entry)

		// Store unfiltered client for admin UI tool listing
		rsClient.enabled = true
		mcpServer.remoteClients[remoteServer.Namespace] = rsClient
	}

	mcpServer.pendingStaticEntries = entries

	return mcpServer, nil
}

// createRemoteServerEntry creates a RemoteServerEntry and remoteServerClient for a server config
// If storageServer is provided, it's used for disabled tools; otherwise static config is used
func (m *MCPServer) createRemoteServerEntry(config types.MCPRemoteServerConfig, storageServer *storage.MCPServerConfig) (mcp.RemoteServerEntry, *remoteServerClient) {
	// stdio server: a local executable launched as a subprocess. No URL/auth.
	if config.Command != "" {
		client, err := mcp.NewStdioClient(config.Command, config.Args, config.Namespace, mcp.WithClientExtraEnv(config.Env...))
		if err != nil {
			m.logger.Warn("failed to launch stdio MCP server", "namespace", config.Namespace, "command", config.Command, "error", err)
			// Return an empty entry; ReloadAllServers skips nil clients via the
			// unfiltered rsClient still being usable for admin listing attempts.
			return mcp.RemoteServerEntry{Visibility: mcp.ToolVisibilityNative}, &remoteServerClient{config: config}
		}
		// Unfiltered client for admin UI listing: a second stdio subprocess is
		// wasteful, so reuse the same client (the filter only affects listing in
		// the federated path; admin listing reads the full set directly).
		unfilteredClient, _ := mcp.NewStdioClient(config.Command, config.Args, config.Namespace, mcp.WithClientExtraEnv(config.Env...))

		visibility := mcp.ToolVisibilityNative
		if config.ToolVisibility == "ondemand" || config.ToolVisibility == "discoverable" {
			visibility = mcp.ToolVisibilityDiscoverable
		}

		if config.Notifications {
			client.EnableNotifications()
		}

		rsClient := &remoteServerClient{
			client:      unfilteredClient,
			config:      config,
			initialized: false,
		}
		return mcp.RemoteServerEntry{
			Client:       client,
			Visibility:   visibility,
			RemoteSearch: config.RemoteSearch,
		}, rsClient
	}

	var auth mcp.AuthProvider
	if config.AuthType == "oauth2" {
		auth = mcp.NewOAuth2RefreshTokenAuth(config.OAuthTokenURL, config.OAuthClientID, config.OAuthAccessToken, config.OAuthRefreshToken)
	} else if config.Token != "" {
		auth = mcp.NewBearerTokenAuth(config.Token)
	}

	// Normalize URL by removing trailing slash
	normalizedURL := strings.TrimSuffix(config.URL, "/")

	// Create client for MCP server registration (may be filtered)
	client := mcp.NewClient(normalizedURL, auth, config.Namespace)
	declareUIAppsSupport(client)

	// Create a separate unfiltered client for admin UI tool listing
	unfilteredClient := mcp.NewClient(normalizedURL, auth, config.Namespace)
	declareUIAppsSupport(unfilteredClient)

	// Determine visibility
	visibility := mcp.ToolVisibilityNative
	if config.ToolVisibility == "ondemand" || config.ToolVisibility == "discoverable" {
		visibility = mcp.ToolVisibilityDiscoverable
	}

	// Apply tool filter
	if len(config.ToolAllowlist) > 0 || len(config.ToolDenylist) > 0 || (storageServer != nil && len(storageServer.DisabledTools) > 0) {
		allowlist := config.ToolAllowlist
		denylist := config.ToolDenylist
		var disabledTools []string
		if storageServer != nil {
			disabledTools = storageServer.DisabledTools
		}
		client = client.WithToolFilter(func(toolName string) bool {
			// Check allowlist first
			if len(allowlist) > 0 {
				if !slices.Contains(allowlist, toolName) {
					return false
				}
			}
			// Check denylist
			if len(denylist) > 0 {
				if slices.Contains(denylist, toolName) {
					return false
				}
			}
			// Check disabled tools (storage-based only)
			if len(disabledTools) > 0 {
				if slices.Contains(disabledTools, toolName) {
					return false
				}
			}
			return true
		})
	}

	// Opt the federated client into notifications: it opens an SSE reader and,
	// via the propagation hook installed at registration, refreshes our merged
	// tool cache and re-emits listChanged to our own clients when the remote's
	// tools change. (Only for the federated client; the unfiltered admin client
	// refreshes on demand instead.)
	if config.Notifications {
		client.EnableNotifications()
	}

	rsClient := &remoteServerClient{
		client:      unfilteredClient,
		config:      config,
		initialized: false,
	}

	return mcp.RemoteServerEntry{
		Client:       client,
		Visibility:   visibility,
		RemoteSearch: config.RemoteSearch,
	}, rsClient
}

func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	m.endpointServer.HandleRequest(w, r)
}

// ReloadAllServers atomically replaces all remote servers (static + storage-based)
// on both MCP servers:
//
//   - m.server is the chat-side view: every enabled remote server, apps
//     included — the web chat and scripts resolve their tools through it
//     and mount app views via lmchatkit's app-proxy.
//   - m.endpointServer is the public /mcp endpoint: llmrouter's own tools
//     plus only the servers opted in with federate = true, and never their
//     app tools (ExcludeApps) — a federated app view would call its own
//     bare, host-agnostic tool names, which the namespaced endpoint can't
//     resolve.
func (m *MCPServer) ReloadAllServers(storageServers []*storage.MCPServerConfig) {
	entries := make([]mcp.RemoteServerEntry, 0, len(m.pendingStaticEntries)+len(storageServers))
	endpointEntries := make([]mcp.RemoteServerEntry, 0, len(m.pendingStaticEntries)+len(storageServers))

	// Clear existing remote clients
	m.remoteClients = make(map[string]*remoteServerClient)

	// Add static servers from config
	for _, remoteServer := range m.config.MCP.RemoteServers {
		entry, rsClient := m.createRemoteServerEntry(remoteServer, nil)
		rsClient.enabled = true
		entries = append(entries, entry)
		if remoteServer.Federate {
			endpointEntry := entry
			endpointEntry.ExcludeApps = true
			endpointEntries = append(endpointEntries, endpointEntry)
		}
		m.remoteClients[remoteServer.Namespace] = rsClient
		m.logger.Info("registering static MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "federate", remoteServer.Federate)
	}

	// Add storage-based servers (only enabled ones are registered with MCP)
	for _, server := range storageServers {
		config := types.MCPRemoteServerConfig{
			Namespace:         server.Namespace,
			URL:               server.URL,
			Command:           server.Command,
			Args:              server.Args,
			Env:               server.Env,
			AuthType:          server.AuthType,
			Token:             server.Token,
			OAuthClientID:     server.OAuthClientID,
			OAuthTokenURL:     server.OAuthTokenURL,
			OAuthAccessToken:  server.OAuthAccessToken,
			OAuthRefreshToken: server.OAuthRefreshToken,
			ToolVisibility:    server.ToolVisibility,
			ToolAllowlist:     server.ToolAllowlist,
			ToolDenylist:      server.ToolDenylist,
			RemoteSearch:      server.RemoteSearch,
			Notifications:     server.Notifications,
			Federate:          server.Federate,
		}
		entry, rsClient := m.createRemoteServerEntry(config, server)
		rsClient.enabled = server.Enabled
		m.remoteClients[server.Namespace] = rsClient

		// Only register enabled servers with the MCP server
		if server.Enabled {
			entries = append(entries, entry)
			if server.Federate {
				endpointEntry := entry
				endpointEntry.ExcludeApps = true
				endpointEntries = append(endpointEntries, endpointEntry)
			}
			if server.Command != "" {
				m.logger.Info("registering storage-based MCP server", "namespace", server.Namespace, "command", server.Command)
			} else {
				m.logger.Info("registering storage-based MCP server", "namespace", server.Namespace, "url", server.URL)
			}
		} else {
			m.logger.Info("skipping disabled storage-based MCP server", "namespace", server.Namespace, "url", server.URL, "command", server.Command)
		}
	}

	if err := m.server.ReplaceRemoteServers(entries); err != nil {
		m.logger.Warn("failed to replace remote MCP servers", "error", err)
	}
	if err := m.endpointServer.ReplaceRemoteServers(endpointEntries); err != nil {
		m.logger.Warn("failed to replace remote MCP servers on the /mcp endpoint", "error", err)
	}

	// The federated tool set just changed (servers added/removed/replaced): tell
	// connected clients to drop their cached tool list and re-fetch.
	m.server.NotifyToolsChanged()
	m.endpointServer.NotifyToolsChanged()

	m.logger.Info("reloaded MCP servers", "static", len(m.config.MCP.RemoteServers), "storage", len(storageServers))
}

// toolAdminMeta extracts a tool's icons and "is this an MCP app" flag for
// the admin UI, using the library's own app check (_meta.ui.resourceUri).
func toolAdminMeta(tool mcp.MCPTool) ([]admin.Icon, bool) {
	var icons []admin.Icon
	if len(tool.Icons) > 0 {
		icons = make([]admin.Icon, 0, len(tool.Icons))
		for _, ic := range tool.Icons {
			icons = append(icons, admin.Icon{Src: ic.Src, MimeType: ic.MimeType, Sizes: ic.Sizes, Theme: ic.Theme})
		}
	}

	return icons, mcp.ToolIsApp(tool)
}

// GetProtocolVersionForAdmin returns the protocol version namespace's remote
// server actually negotiated (e.g. "2025-06-18" for a Legacy server, or the
// Modern era's fixed revision), connecting lazily if not already initialized.
// Returns "" (not an error) if the namespace is unknown or the connection
// attempt fails — the admin UI treats an empty version as "unavailable"
// rather than surfacing a connection error on every server card.
func (m *MCPServer) GetProtocolVersionForAdmin(namespace string) (string, error) {
	rsClient, exists := m.remoteClients[namespace]
	if !exists || rsClient.client == nil {
		return "", nil
	}
	if err := rsClient.ensureInitialized(context.Background()); err != nil {
		m.logger.Warn("failed to initialize MCP client for protocol version lookup", "namespace", namespace, "url", rsClient.config.URL, "error", err)
		return "", nil
	}
	return rsClient.client.ProtocolVersion(), nil
}

// GetToolsForAdmin returns tools for a specific namespace for the admin UI
// This fetches ALL tools from the remote server (not filtered) and calculates enabled state
func (m *MCPServer) GetToolsForAdmin(namespace string) ([]admin.ToolInfo, error) {
	result := make([]admin.ToolInfo, 0)

	// Find the remote client for this namespace
	rsClient, exists := m.remoteClients[namespace]
	if !exists {
		return result, nil
	}

	// Ensure client is initialized (lazy initialization)
	// If initialization fails, return empty list gracefully (matches static server behavior)
	ctx := context.Background()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		m.logger.Warn("failed to initialize MCP client for tools listing", "namespace", namespace, "url", rsClient.config.URL, "error", err)
		return result, nil
	}

	// Refresh the tool cache to ensure we get the latest tools from the remote server
	if err := rsClient.client.RefreshToolCache(ctx); err != nil {
		m.logger.Warn("failed to refresh tool cache from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	// Fetch all tools directly from the remote server (unfiltered)
	tools, err := rsClient.client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	// Get allowlist/denylist from config
	allowlist := rsClient.config.ToolAllowlist
	denylist := rsClient.config.ToolDenylist

	prefix := namespace + mcp.DefaultNamespaceSeparator
	for _, tool := range tools {
		if !strings.HasPrefix(tool.Name, prefix) {
			continue
		}

		// Extract tool name without namespace prefix for list checking
		toolNameWithoutPrefix := strings.TrimPrefix(tool.Name, prefix)

		// Calculate enabled state based on allowlist/denylist
		enabled := true
		if len(allowlist) > 0 {
			// If allowlist is defined, tool is enabled only if in the list
			enabled = slices.Contains(allowlist, toolNameWithoutPrefix)
		} else if len(denylist) > 0 {
			// If denylist is defined, tool is enabled unless in the list
			enabled = !slices.Contains(denylist, toolNameWithoutPrefix)
		}

		var inputSchema map[string]interface{}
		switch s := tool.InputSchema.(type) {
		case map[string]interface{}:
			inputSchema = s
		default:
			if b, err := json.Marshal(tool.InputSchema); err == nil {
				json.Unmarshal(b, &inputSchema)
			}
		}
		if inputSchema == nil {
			inputSchema = make(map[string]interface{})
		}

		icons, isApp := toolAdminMeta(tool)
		result = append(result, admin.ToolInfo{
			Name:        toolNameWithoutPrefix,
			Description: tool.Description,
			InputSchema: inputSchema,
			Enabled:     enabled,
			Icons:       icons,
			IsApp:       isApp,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// CallToolForAdmin executes a tool on the remote server behind namespace for the
// admin UI. toolName is the unprefixed name (as shown in the UI); the remote
// client strips/ignores the namespace itself. args is forwarded verbatim.
func (m *MCPServer) CallToolForAdmin(namespace, toolName string, args map[string]any) (*admin.ToolCallResult, error) {
	rsClient, exists := m.remoteClients[namespace]
	if !exists {
		return nil, fmt.Errorf("MCP server %q not found", namespace)
	}

	ctx := context.Background()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to MCP server: %w", err)
	}

	resp, err := rsClient.client.CallTool(ctx, toolName, args)
	if err != nil {
		return nil, err
	}

	result := &admin.ToolCallResult{}
	for _, c := range resp.Content {
		result.Content = append(result.Content, admin.ToolCallContent{
			Type:     c.Type,
			Text:     c.Text,
			Data:     c.Data,
			MimeType: c.MimeType,
		})
	}
	result.StructuredContent = resp.StructuredContent
	return result, nil
}
func (m *MCPServer) GetStorageServerTools(namespace string, server *storage.MCPServerConfig) ([]admin.ToolInfo, error) {
	result := make([]admin.ToolInfo, 0)

	// Find the remote client for this namespace
	rsClient, exists := m.remoteClients[namespace]
	if !exists {
		return result, nil
	}

	// Ensure client is initialized (lazy initialization)
	// If initialization fails, return empty list gracefully (matches static server behavior)
	ctx := context.Background()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		m.logger.Warn("failed to initialize MCP client for tools listing", "namespace", namespace, "url", rsClient.config.URL, "error", err)
		return result, nil
	}

	// Refresh the tool cache to ensure we get the latest tools from the remote server
	if err := rsClient.client.RefreshToolCache(ctx); err != nil {
		m.logger.Warn("failed to refresh tool cache from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	// Fetch all tools directly from the remote server (unfiltered)
	tools, err := rsClient.client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	prefix := namespace + mcp.DefaultNamespaceSeparator
	for _, tool := range tools {
		if !strings.HasPrefix(tool.Name, prefix) {
			continue
		}

		// Extract tool name without namespace prefix
		toolNameWithoutPrefix := strings.TrimPrefix(tool.Name, prefix)

		// Check if tool is enabled using the storage config
		enabled := server.IsToolEnabled(toolNameWithoutPrefix)

		var inputSchema map[string]interface{}
		switch s := tool.InputSchema.(type) {
		case map[string]interface{}:
			inputSchema = s
		default:
			if b, err := json.Marshal(tool.InputSchema); err == nil {
				json.Unmarshal(b, &inputSchema)
			}
		}
		if inputSchema == nil {
			inputSchema = make(map[string]interface{})
		}

		icons, isApp := toolAdminMeta(tool)
		result = append(result, admin.ToolInfo{
			Name:        toolNameWithoutPrefix,
			Description: tool.Description,
			InputSchema: inputSchema,
			Enabled:     enabled,
			Icons:       icons,
			IsApp:       isApp,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// GetResourcesForAdmin returns resources (static + templates) exposed by the
// remote server behind namespace. Resources are read-only in the UI — the
// router has no per-resource enable/disable toggle the way it does for tools.
// Static resources and templates are merged into a single list; templates carry
// Template=true and a URITemplate in URI. The namespace prefix is stripped so
// the UI shows the upstream names.
func (m *MCPServer) GetResourcesForAdmin(namespace string) ([]admin.ResourceInfo, error) {
	result := make([]admin.ResourceInfo, 0)

	rsClient, exists := m.remoteClients[namespace]
	if !exists {
		return result, nil
	}

	ctx := context.Background()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		m.logger.Warn("failed to initialize MCP client for resources listing", "namespace", namespace, "url", rsClient.config.URL, "error", err)
		return result, nil
	}

	prefix := namespace + mcp.DefaultNamespaceSeparator

	// The authoritative record of which resources are skill files is the
	// server's skills/list (the spec forbids inferring skill-ness from the
	// URI scheme alone), so cross-reference it before listing.
	skillURIs := map[string]bool{}
	if skills, err := rsClient.client.ListSkills(ctx); err == nil {
		for _, skill := range skills {
			for _, res := range skill.Resources {
				skillURIs[res.URI] = true
			}
		}
	}

	static, err := rsClient.client.ListResources(ctx)
	if err != nil {
		m.logger.Warn("failed to list resources from remote server", "namespace", namespace, "error", err)
		// Continue to templates rather than bailing — some servers expose only
		// templates and silence resources/list, others do the opposite.
	}
	for _, r := range static {
		uri := strings.TrimPrefix(r.URI, prefix)
		result = append(result, admin.ResourceInfo{
			URI:         uri,
			Template:    false,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
			Skill:       skillURIs[r.URI],
		})
	}

	templates, err := rsClient.client.ListResourceTemplates(ctx)
	if err != nil {
		m.logger.Warn("failed to list resource templates from remote server", "namespace", namespace, "error", err)
	}
	for _, t := range templates {
		uri := strings.TrimPrefix(t.URITemplate, prefix)
		result = append(result, admin.ResourceInfo{
			URI:         uri,
			Template:    true,
			Name:        t.Name,
			Description: t.Description,
			MimeType:    t.MimeType,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Template != result[j].Template {
			return !result[i].Template // static first, then templates
		}
		return result[i].URI < result[j].URI
	})
	return result, nil
}

// GetPromptsForAdmin returns prompts exposed by the remote server behind
// namespace. Like resources, prompts are read-only in the UI.
func (m *MCPServer) GetPromptsForAdmin(namespace string) ([]admin.PromptInfo, error) {
	result := make([]admin.PromptInfo, 0)

	rsClient, exists := m.remoteClients[namespace]
	if !exists {
		return result, nil
	}

	ctx := context.Background()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		m.logger.Warn("failed to initialize MCP client for prompts listing", "namespace", namespace, "url", rsClient.config.URL, "error", err)
		return result, nil
	}

	prompts, err := rsClient.client.ListPrompts(ctx)
	if err != nil {
		m.logger.Warn("failed to list prompts from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	prefix := namespace + mcp.DefaultNamespaceSeparator
	for _, p := range prompts {
		name := strings.TrimPrefix(p.Name, prefix)
		info := admin.PromptInfo{
			Name:        name,
			Description: p.Description,
		}
		for _, arg := range p.Arguments {
			info.Arguments = append(info.Arguments, admin.PromptArgument{
				Name:        arg.Name,
				Description: arg.Description,
				Required:    arg.Required,
			})
		}
		result = append(result, info)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// ShutdownScriptlingTools shuts down the scriptling tool manager
func (m *MCPServer) ShutdownScriptlingTools() {
	if m.scriptlingManager != nil {
		m.scriptlingManager.Shutdown()
	}
}

// NewMCPServerWithScriptling creates a new MCP server with scriptling tool support
func NewMCPServerWithScriptling(config *types.Config, logger Logger) (*MCPServer, error) {
	mcpServer, err := NewMCPServer(config, logger)
	if err != nil {
		return nil, err
	}

	// Set up scriptling-served MCP content if any source folder is configured.
	// Registered on both servers: llmrouter's own tools/resources/prompts are
	// native content on the chat-side server and the public /mcp endpoint.
	if config.Scripting.ToolsDir != "" || config.Scripting.ResourcesDir != "" || config.Scripting.PromptsDir != "" || config.Scripting.SkillsDir != "" || config.Scripting.ExecScript {
		manager, err := NewScriptlingToolManager(config.Scripting, logger, mcpServer.server, mcpServer.endpointServer)
		if err != nil {
			logger.Warn("Failed to setup scriptling tools", "error", err)
		} else {
			mcpServer.scriptlingManager = manager
			if config.Scripting.ExecScript {
				logger.Info("Scriptling execute_script tool enabled")
			}
			logger.Info("Scriptling MCP content enabled",
				"tools_dir", config.Scripting.ToolsDir,
				"resources_dir", config.Scripting.ResourcesDir,
				"prompts_dir", config.Scripting.PromptsDir,
				"skills_dir", config.Scripting.SkillsDir)
		}
	}

	return mcpServer, nil
}

// ReadResourceForAdmin reads one resource from the remote server behind
// namespace, for the admin UI's viewer popup. uri is the upstream name as
// shown in the listing (the namespace prefix the client adds on listing is
// not part of the remote's own URI).
func (m *MCPServer) ReadResourceForAdmin(namespace, uri string) (*admin.ResourceReadResult, error) {
	rsClient, exists := m.remoteClients[namespace]
	if !exists || rsClient.client == nil {
		return nil, fmt.Errorf("MCP server %q not found", namespace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := rsClient.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to MCP server: %w", err)
	}
	resp, err := rsClient.client.ReadResource(ctx, uri)
	if err != nil {
		return nil, err
	}
	result := &admin.ResourceReadResult{URI: uri}
	if len(resp.Contents) > 0 {
		result.MimeType = resp.Contents[0].MimeType
		result.Text = resp.Contents[0].Text
	}
	return result, nil
}
