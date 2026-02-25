package router

import (
	"net/http"

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
