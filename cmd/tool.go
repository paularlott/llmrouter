package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/log"
)

// serverTools are tools registered directly on the MCP server
var serverTools = map[string]bool{
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
		&cli.StringFlag{
			Name:    "token",
			Aliases: []string{"t"},
			Usage:   "Bearer token for server authentication",
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		toolName := cmd.GetStringArg("toolname")
		argsStr := cmd.GetStringArg("arguments")
		serverURL := cmd.GetString("server")
		verbose := cmd.GetBool("verbose")
		token := cmd.GetString("token")

		var toolArgs map[string]interface{}
		if argsStr != "" {
			if err := json.Unmarshal([]byte(argsStr), &toolArgs); err != nil {
				return fmt.Errorf("error parsing arguments: %w\nHint: Quote your JSON string properly", err)
			}
		}

		if verbose {
			log.GetLogger().Debug("executing tool", "tool", toolName, "args", toolArgs)
		}

		var request map[string]interface{}
		if serverTools[toolName] {
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

		return ExecuteMCPRequest(serverURL, request, token, verbose)
	},
}

// ExecuteMCPRequest sends an MCP request and processes the response
func ExecuteMCPRequest(serverURL string, request map[string]interface{}, token string, verbose bool) error {
	logger := log.GetLogger()

	requestBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	if verbose {
		logger.Debug("MCP request", "request", string(requestBody))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", serverURL+"/mcp", bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if verbose {
		logger.Debug("MCP response", "status", resp.Status, "response", string(responseBody))
	}

	var response map[string]interface{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if jsonrpcError, ok := response["error"].(map[string]interface{}); ok {
		message, _ := jsonrpcError["message"].(string)
		return fmt.Errorf("MCP error: %s", message)
	}

	if result, ok := response["result"].(map[string]interface{}); ok {
		if content, ok := result["content"].([]interface{}); ok {
			for _, item := range content {
				if contentItem, ok := item.(map[string]interface{}); ok {
					if text, ok := contentItem["text"].(string); ok {
						fmt.Print(text)
					}
				}
			}
		}
	}

	return nil
}
