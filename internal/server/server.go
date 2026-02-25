package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/internal/router"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/log"
	"github.com/paularlott/mcp/pool"
)

func RunServer(ctx context.Context, cmd *cli.Command) error {
	config := &types.Config{
		Server: types.ServerConfig{
			Host:  cmd.GetString("host"),
			Port:  cmd.GetInt("port"),
			Token: cmd.GetString("token"),
		},
		Logging: types.LoggingConfig{
			Level:  cmd.GetString("log-level"),
			Format: cmd.GetString("log-format"),
		},
		Providers: []types.ProviderConfig{},
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{},
		},
		Storage: types.StorageConfig{
			Path: cmd.GetString("storage-path"),
		},
		Responses: types.ResponsesConfig{
			TTLDays: cmd.GetInt("responses-ttl"),
		},
		Conversations: types.ConversationsConfig{
			TTLDays: cmd.GetInt("conversations-ttl"),
		},
	}

	log.Configure(config.Logging.Level, config.Logging.Format)
	logger := log.GetLogger()
	logger.Info("starting LLM router", "version", "1.0.0")

	pool.SetPoolConfig(&pool.PoolConfig{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		Timeout:             30 * time.Second,
		InsecureSkipVerify:  false,
	})

	if cmd.ConfigFile != nil {
		typedConfig := cli.NewTypedConfigFile(cmd.ConfigFile)
		for _, pc := range typedConfig.GetObjectSlice("providers") {
			config.Providers = append(config.Providers, types.ProviderConfig{
				Name:      pc.GetString("name"),
				BaseURL:   strings.TrimSuffix(pc.GetString("base_url"), "/"),
				Token:     pc.GetString("token"),
				Enabled:   pc.GetBool("enabled"),
				Models:    pc.GetStringSlice("models"),
				Allowlist: pc.GetStringSlice("allowlist"),
				Denylist:  pc.GetStringSlice("denylist"),
			})
		}
		if mcpCfg := typedConfig.GetObject("mcp"); mcpCfg != nil {
			for _, sc := range mcpCfg.GetObjectSlice("remote_servers") {
				config.MCP.RemoteServers = append(config.MCP.RemoteServers, types.MCPRemoteServerConfig{
					Namespace:      sc.GetString("namespace"),
					URL:            strings.TrimSuffix(sc.GetString("url"), "/"),
					Token:          sc.GetString("token"),
					ToolVisibility: sc.GetString("tool_visibility"),
				})
			}
		}
	}

	logger.Info("loaded providers from config", "count", len(config.Providers))

	r, err := router.NewRouter(config, logger)
	if err != nil {
		logger.Error("failed to create router", "error", err)
		return err
	}

	r.StartBackgroundTasks()
	defer r.StopBackgroundTasks()

	if err := r.RefreshModels(ctx); err != nil {
		logger.Warn("initial model refresh failed", "error", err)
	}

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server listening", "host", config.Server.Host, "port", config.Server.Port)
		if err := http.ListenAndServe(fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port), r); err != nil {
			logger.Error("server error", "error", err)
		}
	}()

	<-shutdownChan
	logger.Info("shutting down server")
	r.Shutdown()
	logger.Info("server stopped")
	return nil
}
