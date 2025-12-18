package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/log"
)

// serverTools are tools registered directly on the MCP server (not via discovery registry)
var serverTools = map[string]bool{
	"execute_code": true,
	"execute_tool": true,
	"tool_search":  true,
}

var ToolCmd = &cli.Command{
	Name:        "tool",
	Usage:       "Execute a tool via the MCP server",
	Description: "Execute a specific tool through the MCP server",
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:     "toolname",
			Required: true,
			Usage:    "Name of the tool to execute",
		},
		&cli.StringArg{
			Name:     "arguments",
			Required: false,
			Usage:    "JSON arguments for the tool (optional)",
		},
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:         "server",
			Usage:        "MCP server URL",
			DefaultValue: "http://localhost:12345",
		},
		&cli.BoolFlag{
			Name:         "verbose",
			Aliases:      []string{"v"},
			Usage:        "Enable verbose output",
			DefaultValue: false,
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		toolName := cmd.GetStringArg("toolname")
		argsStr := cmd.GetStringArg("arguments")
		serverURL := cmd.GetString("server")
		verbose := cmd.GetBool("verbose")

		var toolArgs map[string]interface{}
		if argsStr != "" {
			if err := json.Unmarshal([]byte(argsStr), &toolArgs); err != nil {
				return fmt.Errorf("error parsing arguments: %w\nHint: Quote your JSON string properly", err)
			}
		}

		logger := log.GetLogger()

		if verbose {
			logger.Debug("executing tool",
				"tool", toolName,
				"args", toolArgs)
		}

		var request map[string]interface{}

		// Check if this is a server-level tool or a discoverable tool
		if serverTools[toolName] {
			// Direct call to server tool
			request = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params": map[string]interface{}{
					"name":      toolName,
					"arguments": toolArgs,
				},
			}
		} else {
			// Call via execute_tool for discoverable tools
			request = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params": map[string]interface{}{
					"name": "execute_tool",
					"arguments": map[string]interface{}{
						"name":      toolName,
						"arguments": toolArgs,
					},
				},
			}
		}

		return ExecuteMCPRequest(serverURL, request, verbose)
	},
}
