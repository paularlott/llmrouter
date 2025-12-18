package cmd

import (
	"context"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/internal/server"
)

var ServerCmd = &cli.Command{
	Name:        "server",
	Usage:       "Start the LLM router server",
	Description: "Start the LLM router server with MCP and OpenAI API endpoints",
	Flags: []cli.Flag{
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
			Name:         "tools-path",
			Usage:        "Path to the tools directory",
			DefaultValue: "./tools",
			ConfigPath:   []string{"scriptling.tools_path"},
		},
		&cli.StringFlag{
			Name:         "libs-path",
			Usage:        "Path to the libraries directory",
			DefaultValue: "./libs",
			ConfigPath:   []string{"scriptling.libraries_path"},
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		return server.RunServer(ctx, cmd)
	},
}
