package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/paularlott/llmrouter/build"
	"github.com/paularlott/llmrouter/cmd"
	"github.com/paularlott/llmrouter/log"

	"github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
)

var configFile = "config.toml"

func main() {
	rootCmd := &cli.Command{
		Name:        "llmrouter",
		Version:     build.Version,
		Usage:       "LLM Routing Service",
		Description: "Routes requests to different LLM providers based on configuration",
		ConfigFile: cli_toml.NewConfigFile(&configFile, func() []string {
			paths := []string{"."}
			home, err := os.UserHomeDir()
			if err == nil {
				paths = append(paths, home)
				paths = append(paths, filepath.Join(home, ".config"))
				paths = append(paths, filepath.Join(home, ".config", "llmrouter"))
			}
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
			log.Configure(cmd.GetString("log-level"), cmd.GetString("log-format"))
			return ctx, nil
		},
		Commands: []*cli.Command{
			cmd.ServerCmd,
			cmd.ToolCmd,
			cmd.ModelsCmd,
			cmd.AskCmd,
		},
	}

	if err := rootCmd.Execute(context.Background()); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
