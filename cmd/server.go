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
			Name:       "token",
			Aliases:    []string{"t"},
			Usage:      "Bearer token for API authentication",
			ConfigPath: []string{"server.token"},
		},
		&cli.StringFlag{
			Name:       "storage-path",
			Usage:      "Path for persistent storage (omit for memory-only)",
			ConfigPath: []string{"storage.path"},
		},
		&cli.IntFlag{
			Name:         "responses-ttl",
			Usage:        "Maximum age of a response in days",
			ConfigPath:   []string{"responses.ttl_days"},
			DefaultValue: 30,
		},
		&cli.IntFlag{
			Name:         "conversations-ttl",
			Usage:        "Maximum age of a conversation in days",
			ConfigPath:   []string{"conversations.ttl_days"},
			DefaultValue: 30,
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		return server.RunServer(ctx, cmd)
	},
}
