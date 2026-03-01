package router

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
	"slices"
)

// remoteServerClient holds a client and its config for admin UI tool listing
type remoteServerClient struct {
	client   *mcp.Client
	config   types.MCPRemoteServerConfig
}

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server         *mcp.Server
	config         *types.Config
	logger         Logger
	remoteClients  map[string]*remoteServerClient // namespace -> client
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

	for _, remoteServer := range config.MCP.RemoteServers {
		var auth mcp.AuthProvider
		if remoteServer.Token != "" {
			auth = mcp.NewBearerTokenAuth(remoteServer.Token)
		}

		// Create client for MCP server registration (may be filtered)
		client := mcp.NewClient(remoteServer.URL, auth, remoteServer.Namespace)

		// Create a separate unfiltered client for admin UI tool listing
		unfilteredClient := mcp.NewClient(remoteServer.URL, auth, remoteServer.Namespace)
		mcpServer.remoteClients[remoteServer.Namespace] = &remoteServerClient{
			client: unfilteredClient,
			config: remoteServer,
		}

		// Apply tool filter to the MCP server client (not the admin UI client)
		if len(remoteServer.ToolAllowlist) > 0 || len(remoteServer.ToolDenylist) > 0 {
			allowlist := remoteServer.ToolAllowlist
			denylist := remoteServer.ToolDenylist
			client = client.WithToolFilter(func(toolName string) bool {
				if len(allowlist) > 0 {
					// If allowlist is defined, tool is included only if in the list
					return slices.Contains(allowlist, toolName)
				}
				if len(denylist) > 0 {
					// If denylist is defined, tool is included unless in the list
					return !slices.Contains(denylist, toolName)
				}
				return true
			})
		}

		if remoteServer.ToolVisibility == "ondemand" || remoteServer.ToolVisibility == "discoverable" {
			if err := server.RegisterRemoteServerDiscoverable(client); err != nil {
				logger.Warn("failed to connect to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "error", err)
			} else {
				logger.Info("connected to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "visibility", "discoverable")
			}
		} else {
			if err := server.RegisterRemoteServer(client); err != nil {
				logger.Warn("failed to connect to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "error", err)
			} else {
				logger.Info("connected to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "visibility", "native")
			}
		}
	}

	return mcpServer, nil
}

func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	m.server.HandleRequest(w, r)
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

	// Fetch all tools directly from the remote server (unfiltered)
	ctx := context.Background()
	tools, err := rsClient.client.ListTools(ctx)
	if err != nil {
		return nil, err
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
