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
		&cli.StringFlag{
			Name:       "routes-dir",
			Usage:      "Directory of smart-router <model>.toml/.py pairs (traffic for <model> is routed by <model>.py)",
			ConfigPath: []string{"routes_dir"},
		},
		&cli.StringFlag{
			Name:         "tools-dir",
			Usage:        "Directory containing scriptling tool definitions (.toml/.py pairs)",
			ConfigPath:   []string{"scripting.tools_dir"},
		},
		&cli.StringFlag{
			Name:         "resources-dir",
			Usage:        "Directory containing scriptling MCP resources (static files and {var}.py templates)",
			ConfigPath:   []string{"scripting.resources_dir"},
		},
		&cli.StringFlag{
			Name:         "prompts-dir",
			Usage:        "Directory containing scriptling MCP prompts (.toml+.py dynamic, or .md/.txt static)",
			ConfigPath:   []string{"scripting.prompts_dir"},
		},
		&cli.StringSliceFlag{
			Name:       "plugin-dir",
			Usage:      "Directory containing scriptling plugin executables (can be repeated)",
			ConfigPath: []string{"scripting.plugin_dirs"},
		},
		&cli.StringSliceFlag{
			Name:       "libpath",
			Usage:      "Additional directories to search for scriptling libraries (can be repeated)",
			ConfigPath: []string{"scripting.lib_paths"},
		},
		&cli.BoolFlag{
			Name:       "mcp-exec-script",
			Usage:      "Enable the built-in execute_script MCP tool (runs Scriptling code passed by the caller)",
			ConfigPath: []string{"scripting.exec_script"},
		},
		&cli.StringFlag{
			Name:         "personas-dir",
			Usage:        "Directory of chat persona .toml files (system_prompt, default_model, [params] table)",
			ConfigPath:   []string{"chat.personas_dir"},
		},
		&cli.StringFlag{
			Name:         "commands-dir",
			Usage:        "Directory of slash-command .md files (use $ARGUMENTS to splice user input)",
			ConfigPath:   []string{"chat.commands_dir"},
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		return server.RunServer(ctx, cmd)
	},
}
