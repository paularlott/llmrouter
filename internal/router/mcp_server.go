package router

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
)

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server *mcp.Server
	config *types.Config
	logger Logger
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(config *types.Config, logger Logger) (*MCPServer, error) {
	server := mcp.NewServer("llmrouter", "1.0.0")

	mcpServer := &MCPServer{
		server: server,
		config: config,
		logger: logger,
	}

	for _, remoteServer := range config.MCP.RemoteServers {
		var auth mcp.AuthProvider
		if remoteServer.Token != "" {
			auth = mcp.NewBearerTokenAuth(remoteServer.Token)
		}

		client := mcp.NewClient(remoteServer.URL, auth, remoteServer.Namespace)

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
func (m *MCPServer) GetToolsForAdmin(namespace string) ([]admin.ToolInfo, error) {
	tools := m.server.ListTools()
	result := make([]admin.ToolInfo, 0)

	prefix := namespace + "."
	for _, tool := range tools {
		if !strings.HasPrefix(tool.Name, prefix) {
			continue
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
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
