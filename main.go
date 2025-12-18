package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/paularlott/llmrouter/cmd"
	"github.com/paularlott/llmrouter/internal/server"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/log"

	"github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
)

var configFile = "config.toml"

// Type aliases for compatibility
type (
	Config                = types.Config
	ServerConfig          = types.ServerConfig
	LoggingConfig         = types.LoggingConfig
	ProviderConfig        = types.ProviderConfig
	MCPConfig             = types.MCPConfig
	MCPRemoteServerConfig = types.MCPRemoteServerConfig
	ScriptlingConfig      = types.ScriptlingConfig
)

func main() {
	// Set the NewRouter function in the server package
	server.SetNewRouterFunc(func(config *types.Config, logger interface{}) (server.Router, error) {
		return NewRouter((*Config)(config), logger.(Logger))
	})

	// Create the root command
	rootCmd := &cli.Command{
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
				Name:         "log-level",
				Usage:        "Log level (trace|debug|info|warn|error)",
				DefaultValue: "info",
				ConfigPath:   []string{"logging.level"},
				Global:       true,
			},
			&cli.StringFlag{
				Name:         "log-format",
				Usage:        "Log format (console|json)",
				DefaultValue: "console",
				ConfigPath:   []string{"logging.format"},
				Global:       true,
			},
		},
		PreRun: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Setup logging at root level as per knot pattern
			logLevel := cmd.GetString("log-level")
			logFormat := cmd.GetString("log-format")
			log.Configure(logLevel, logFormat)
			return ctx, nil
		},
		Commands: []*cli.Command{
			cmd.ServerCmd,
			cmd.ScriptCmd,
			cmd.ToolCmd,
		},
	}

	err := rootCmd.Execute(context.Background())
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	os.Exit(0)
}
