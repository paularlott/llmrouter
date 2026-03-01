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
			Name:       "admin-password",
			Usage:      "Password for admin UI (if set, enables admin UI at /admin)",
			ConfigPath: []string{"server.admin_password"},
		},
		&cli.StringFlag{
			Name:       "storage-path",
			Usage:      "Path for persistent storage (omit for memory-only)",
			ConfigPath: []string{"server.storage_path"},
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
		&cli.BoolFlag{
			Name:       "smart-routing",
			Usage:      "Enable smart routing",
			ConfigPath: []string{"smart_routing.enabled"},
		},
		&cli.StringFlag{
			Name:       "router-script",
			Usage:      "Path to the smart routing script",
			ConfigPath: []string{"smart_routing.script"},
		},
		&cli.StringFlag{
			Name:       "router-libdir",
			Usage:      "Directory of .py script libraries auto-loaded into every routing VM",
			ConfigPath: []string{"smart_routing.libdir"},
		},
		&cli.StringFlag{
			Name:       "router-default-model",
			Usage:      "Default model when smart routing returns nothing",
			ConfigPath: []string{"smart_routing.default_model"},
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		return server.RunServer(ctx, cmd)
	},
}
