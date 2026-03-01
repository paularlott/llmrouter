package router

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

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

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server        *mcp.Server
	config        *types.Config
	logger        Logger
	remoteClients map[string]*remoteServerClient // namespace -> client (for admin UI)
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(config *types.Config, logger Logger) (*MCPServer, error) {
	server := mcp.NewServer("llmrouter", "1.0.0")

	mcpServer := &MCPServer{
		server:        server,
		config:        config,
		logger:        logger,
		remoteClients: make(map[string]*remoteServerClient),
	}

	// Register static servers from config
	entries := make([]mcp.RemoteServerEntry, 0, len(config.MCP.RemoteServers))

	for _, remoteServer := range config.MCP.RemoteServers {
		entry, rsClient := mcpServer.createRemoteServerEntry(remoteServer, nil)
		entries = append(entries, entry)

		// Store unfiltered client for admin UI tool listing
		mcpServer.remoteClients[remoteServer.Namespace] = rsClient
	}

	// Register all static servers at once
	if len(entries) > 0 {
		if err := server.ReplaceRemoteServers(entries); err != nil {
			logger.Warn("failed to register some remote MCP servers", "error", err)
		}
	}

	return mcpServer, nil
}

// createRemoteServerEntry creates a RemoteServerEntry and remoteServerClient for a server config
// If storageServer is provided, it's used for disabled tools; otherwise static config is used
func (m *MCPServer) createRemoteServerEntry(config types.MCPRemoteServerConfig, storageServer *storage.MCPServerConfig) (mcp.RemoteServerEntry, *remoteServerClient) {
	var auth mcp.AuthProvider
	if config.Token != "" {
		auth = mcp.NewBearerTokenAuth(config.Token)
	}

	// Normalize URL by removing trailing slash
	normalizedURL := strings.TrimSuffix(config.URL, "/")

	// Create client for MCP server registration (may be filtered)
	client := mcp.NewClient(normalizedURL, auth, config.Namespace)

	// Create a separate unfiltered client for admin UI tool listing
	unfilteredClient := mcp.NewClient(normalizedURL, auth, config.Namespace)

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

	rsClient := &remoteServerClient{
		client:      unfilteredClient,
		config:      config,
		initialized: false,
	}

	return mcp.RemoteServerEntry{
		Client:     client,
		Visibility: visibility,
	}, rsClient
}

func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	m.server.HandleRequest(w, r)
}

// ReloadAllServers atomically replaces all remote servers (static + storage-based)
// This is the preferred way to update servers when storage changes
func (m *MCPServer) ReloadAllServers(storageServers []*storage.MCPServerConfig) {
	entries := make([]mcp.RemoteServerEntry, 0, len(m.config.MCP.RemoteServers)+len(storageServers))

	// Clear existing remote clients
	m.remoteClients = make(map[string]*remoteServerClient)

	// Add static servers from config
	for _, remoteServer := range m.config.MCP.RemoteServers {
		entry, rsClient := m.createRemoteServerEntry(remoteServer, nil)
		entries = append(entries, entry)
		m.remoteClients[remoteServer.Namespace] = rsClient
		m.logger.Info("registering static MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL)
	}

	// Add storage-based servers (only enabled ones are registered with MCP)
	for _, server := range storageServers {
		config := types.MCPRemoteServerConfig{
			Namespace:      server.Namespace,
			URL:            server.URL,
			Token:          server.Token,
			ToolVisibility: server.ToolVisibility,
			ToolAllowlist:  server.ToolAllowlist,
			ToolDenylist:   server.ToolDenylist,
		}
		entry, rsClient := m.createRemoteServerEntry(config, server)
		m.remoteClients[server.Namespace] = rsClient

		// Only register enabled servers with the MCP server
		if server.Enabled {
			entries = append(entries, entry)
			m.logger.Info("registering storage-based MCP server", "namespace", server.Namespace, "url", server.URL)
		} else {
			m.logger.Info("skipping disabled storage-based MCP server", "namespace", server.Namespace, "url", server.URL)
		}
	}

	// Atomically replace all servers
	if len(entries) > 0 {
		if err := m.server.ReplaceRemoteServers(entries); err != nil {
			m.logger.Warn("failed to replace remote MCP servers", "error", err)
		}
	}

	m.logger.Info("reloaded MCP servers", "static", len(m.config.MCP.RemoteServers), "storage", len(storageServers))
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

	// Fetch all tools directly from the remote server (unfiltered)
	tools, err := rsClient.client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	// Get allowlist/denylist from config
	allowlist := rsClient.config.ToolAllowlist
	denylist := rsClient.config.ToolDenylist

	prefix := namespace + "."
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

		result = append(result, admin.ToolInfo{
			Name:        toolNameWithoutPrefix,
			Description: tool.Description,
			InputSchema: inputSchema,
			Enabled:     enabled,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// GetStorageServerTools returns tools for a storage-based server with disabled state
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

	// Fetch all tools directly from the remote server (unfiltered)
	tools, err := rsClient.client.ListTools(ctx)
	if err != nil {
		m.logger.Warn("failed to list tools from remote server", "namespace", namespace, "error", err)
		return result, nil
	}

	prefix := namespace + "."
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

		result = append(result, admin.ToolInfo{
			Name:        toolNameWithoutPrefix,
			Description: tool.Description,
			InputSchema: inputSchema,
			Enabled:     enabled,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
