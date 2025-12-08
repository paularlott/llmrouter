package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/paularlott/llmrouter/log"

	"github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
)

var configFile = "config.toml"

func main() {
	// Create the root command
	cmd := &cli.Command{
		Name:        "llmrouter",
		Version:     "1.0.0",
		Usage:       "LLM Routing Service",
		Description: "Routes requests to different LLM providers based on configuration",
		ConfigFile: cli_toml.NewConfigFile(&configFile, func() []string {
			// Look for the config file in:
			//   - The current directory
			//   - The user's home directory
			//   - The user's .config directory

			paths := []string{"."}

			home, err := os.UserHomeDir()
			if err == nil {
				paths = append(paths, home)
			}

			paths = append(paths, filepath.Join(home, ".config"))
			paths = append(paths, filepath.Join(home, ".config", "llmrouter"))

			return paths
		}),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "config",
				Aliases:  []string{"c"},
				Usage:    "Configuration file path",
				AssignTo: &configFile,
				Global:   true,
			},
			&cli.StringFlag{
				Name:         "host",
				Aliases:      []string{"H"},
				Usage:        "Host to bind to",
				DefaultValue: "0.0.0.0",
				ConfigPath:   []string{"server.host"},
			},
			&cli.IntFlag{
				Name:         "port",
				Aliases:      []string{"p"},
				Usage:        "Port to bind to",
				DefaultValue: 12345,
				ConfigPath:   []string{"server.port"},
			},
			&cli.StringFlag{
				Name:         "log-level",
				Usage:        "Log level (trace|debug|info|warn|error)",
				DefaultValue: "info",
				ConfigPath:   []string{"logging.level"},
			},
			&cli.StringFlag{
				Name:         "log-format",
				Usage:        "Log format (console|json)",
				DefaultValue: "console",
				ConfigPath:   []string{"logging.format"},
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			// Build configuration from CLI and config file
			config := &Config{
				Server: ServerConfig{
					Host: cmd.GetString("host"),
					Port: cmd.GetInt("port"),
				},
				Logging: LoggingConfig{
					Level:  cmd.GetString("log-level"),
					Format: cmd.GetString("log-format"),
				},
				Providers: []ProviderConfig{},
			}

			// Setup logging first so we can log during provider loading
			log.Configure(config.Logging.Level, config.Logging.Format)
			logger := log.GetLogger()
			logger.Info("starting LLM router", "version", "1.0.0")

			// Load providers from config file if available
			if cmd.ConfigFile != nil {
				providers, exists := cmd.ConfigFile.GetValue("providers")
				if exists {
					if providerList, ok := providers.([]map[string]interface{}); ok {
						for _, providerMap := range providerList {
							provider := ProviderConfig{
								Name:    getString(providerMap, "name"),
								BaseURL: getString(providerMap, "base_url"),
								Token:   getString(providerMap, "token"),
								Enabled: getBool(providerMap, "enabled"),
							}
							config.Providers = append(config.Providers, provider)
						}
						logger.Info("loaded providers from config", "count", len(config.Providers))
					}
				}
			}

			logger.Info("using config file", "path", configFile)

			// Create router
			router, err := NewRouter(config, logger)
			if err != nil {
				logger.WithError(err).Error("failed to create router")
				return err
			}

			// Start background health check task
			router.StartBackgroundTasks()
			defer router.StopBackgroundTasks()

			// Initialize models in background goroutine for faster startup
			go func() {
				initCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				if err := router.RefreshModels(initCtx); err != nil {
					logger.WithError(err).Error("failed to refresh models during startup")
					// Continue running even if model fetch fails - providers might be temporarily down
				}
			}()

			// Setup HTTP server
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/models", router.HandleModels)
			mux.HandleFunc("POST /v1/chat/completions", router.HandleChatCompletions)
			mux.HandleFunc("GET /health", router.HandleHealth)
			mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"service":"llmrouter","version":"1.0.0"}`)
			})

			server := &http.Server{
				Addr:         fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port),
				Handler:      mux,
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 60 * time.Second,
				IdleTimeout:  120 * time.Second,
			}

			// Start server in goroutine
			serverErr := make(chan error, 1)
			go func() {
				logger.Info("server listening", "host", config.Server.Host, "port", config.Server.Port)
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					serverErr <- fmt.Errorf("server failed: %w", err)
				}
			}()

			// Wait for interrupt signal or server error
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

			select {
			case err := <-serverErr:
				return err
			case <-quit:
				logger.Info("shutting down server")
			}

			// Graceful shutdown with timeout
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.WithError(err).Error("server forced to shutdown")
				return err
			}

			logger.Info("server stopped")
			return nil
		},
	}

	// Run the CLI application
	if err := cmd.Execute(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Helper functions to extract values from config file interface{}
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}
