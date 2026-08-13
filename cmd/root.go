package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/paularlott/llmrouter/build"
	"github.com/paularlott/llmrouter/log"

	"github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
)

// configFile is shared by the root command's --config flag and the TOML
// config-file resolver below. Both server and desktop builds read it.
var configFile = "config.toml"

// RootCmd is the top-level command. With no subcommand:
//   - desktop builds (default) open the GUI window via RootCmd.Run (set by
//     init() in root_desktop.go)
//   - server builds (-tags server) show help (Run stays nil — the original
//     behaviour before desktop support was added)
//
// In both builds, `llmrouter server` explicitly starts the headless server.
var RootCmd = &cli.Command{
	Name:        "llmrouter",
	Version:     build.Version,
	Usage:       "LLM Routing Service",
	Description: "Routes requests to different LLM providers based on configuration",
	ConfigFile: cli_toml.NewConfigFile(&configFile, func() []string {
		paths := []string{"."}
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, filepath.Join(home, ".llmrouter"))
			paths = append(paths, filepath.Join(home, ".config", "llmrouter"))
			paths = append(paths, filepath.Join(home, ".config"))
		}
		return paths
	}),
	Flags: append([]cli.Flag{
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
	}, ServerFlags()...),
	PreRun: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		log.Configure(cmd.GetString("log-level"), cmd.GetString("log-format"))
		return ctx, nil
	},
	Commands: []*cli.Command{
		ServerCmd,
		ToolCmd,
		ModelsCmd,
		AskCmd,
	},
}
