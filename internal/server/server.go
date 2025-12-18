package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/log"
)

// RunServer runs the LLM router server with the given configuration
func RunServer(ctx context.Context, cmd *cli.Command) error {
	// Build configuration from CLI and config file
	config := &types.Config{
		Server: types.ServerConfig{
			Host: "0.0.0.0", // Default host
			Port: 12345,     // Default port
		},
		Logging: types.LoggingConfig{
			Level:  cmd.GetString("log-level"),
			Format: cmd.GetString("log-format"),
		},
		Providers: []types.ProviderConfig{},
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{},
		},
		Scriptling: types.ScriptlingConfig{
			ToolsPath:     cmd.GetString("tools-path"),
			LibrariesPath: cmd.GetString("libs-path"),
		},
	}

	// Override with CLI values if provided
	if host := cmd.GetString("host"); host != "" {
		config.Server.Host = host
	}
	if port := cmd.GetInt("port"); port != 0 {
		config.Server.Port = port
	}

	// Setup logging first so we can log during provider loading
	log.Configure(config.Logging.Level, config.Logging.Format)
	logger := log.GetLogger()
	logger.Info("starting LLM router", "version", "1.0.0")

	// Load providers from config file if available
	if cmd.ConfigFile != nil {
		typedConfig := cli.NewTypedConfigFile(cmd.ConfigFile)
		providers := typedConfig.GetObjectSlice("providers")
		for _, providerConfig := range providers {
			provider := types.ProviderConfig{
				Name:      providerConfig.GetString("name"),
				BaseURL:   strings.TrimSuffix(providerConfig.GetString("base_url"), "/"),
				Token:     providerConfig.GetString("token"),
				Enabled:   providerConfig.GetBool("enabled"),
				Models:    providerConfig.GetStringSlice("models"),
				Allowlist: providerConfig.GetStringSlice("allowlist"),
				Denylist:  providerConfig.GetStringSlice("denylist"),
			}
			config.Providers = append(config.Providers, provider)
		}

		// Load MCP config
		mcpConfig := typedConfig.GetObject("mcp")
		if mcpConfig != nil {
			remoteServers := mcpConfig.GetObjectSlice("remote_servers")
			for _, serverConfig := range remoteServers {
				server := types.MCPRemoteServerConfig{
					Namespace: serverConfig.GetString("namespace"),
					URL:       strings.TrimSuffix(serverConfig.GetString("url"), "/"),
					Token:     serverConfig.GetString("token"),
				}
				config.MCP.RemoteServers = append(config.MCP.RemoteServers, server)
			}
		}

		// Load Scriptling config
		scriptlingConfig := typedConfig.GetObject("scriptling")
		if scriptlingConfig != nil {
			if toolsPath := scriptlingConfig.GetString("tools_path"); toolsPath != "" {
				config.Scriptling.ToolsPath = toolsPath
			}
			if libsPath := scriptlingConfig.GetString("libraries_path"); libsPath != "" {
				config.Scriptling.LibrariesPath = libsPath
			}
		}
	}

	logger.Info("loaded providers from config", "count", len(config.Providers))

	// Create router - this will need to be imported from the router package
	router, err := NewRouter(config, logger)
	if err != nil {
		logger.Error("failed to create router", "error", err)
		return err
	}

	// Start background tasks
	router.StartBackgroundTasks()
	defer router.StopBackgroundTasks()

	// Initial model refresh
	if err := router.RefreshModels(ctx); err != nil {
		logger.Warn("initial model refresh failed", "error", err)
	}

	// Setup signal handling for graceful shutdown
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the server
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "host", config.Server.Host, "port", config.Server.Port)
		if err := http.ListenAndServe(fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port), router); err != nil {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal
	<-shutdownChan
	logger.Info("shutting down server")

	// Shutdown router
	router.Shutdown()

	logger.Info("server stopped")

	return nil
}

// Router interface - will be implemented by the router package
type Router interface {
	StartBackgroundTasks()
	StopBackgroundTasks()
	RefreshModels(ctx context.Context) error
	Shutdown()
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// NewRouter function - will be set by main package
var NewRouter func(config *types.Config, logger interface{}) (Router, error)

// SetNewRouterFunc allows main package to set the NewRouter function
func SetNewRouterFunc(fn func(config *types.Config, logger interface{}) (Router, error)) {
	NewRouter = fn
}
