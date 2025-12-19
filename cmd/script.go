package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/log"
)

var ScriptCmd = &cli.Command{
	Name:        "script",
	Usage:       "Execute a script via the MCP server",
	Description: "Execute a Python script through the MCP server",
	MaxArgs:     cli.UnlimitedArgs,
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:     "scriptfile",
			Required: true,
			Usage:    "Path to the script file to execute",
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
		scriptFile := cmd.GetStringArg("scriptfile")
		scriptArgs := cmd.GetArgs()

		// Get flags
		serverURL := cmd.GetString("server")
		verbose := cmd.GetBool("verbose")
		token := cmd.GetString("token")

		// Get logger for verbose output
		logger := log.GetLogger()

		// Read script file
		content, err := os.ReadFile(scriptFile)
		if err != nil {
			return fmt.Errorf("failed to read script file: %w", err)
		}

		scriptContent := string(content)

		// Prepend sys.argv setup to the script
		if len(scriptArgs) > 0 {
			argvSetup := "import sys\nsys.argv = [" + fmt.Sprintf("\"%s\"", scriptFile)
			for _, arg := range scriptArgs {
				argvSetup += fmt.Sprintf(", \"%s\"", strings.ReplaceAll(arg, "\"", "\\\""))
			}
			argvSetup += "]\n\n"
			scriptContent = argvSetup + scriptContent
		}

		if verbose {
			logger.Debug("executing script",
				"file", scriptFile,
				"args", scriptArgs,
				"content_bytes", len(scriptContent))
		}

		// Create MCP request for execute_code
		request := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name": "execute_code",
				"arguments": map[string]string{
					"code": scriptContent,
				},
			},
		}

		return ExecuteMCPRequest(serverURL, request, token, verbose)
	},
}

// ExecuteMCPRequest sends an MCP request and processes the response
func ExecuteMCPRequest(serverURL string, request map[string]interface{}, token string, verbose bool) error {
	logger := log.GetLogger()

	// Marshal request
	requestBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	if verbose {
		logger.Debug("MCP request",
			"request", string(requestBody))
	}

	// Create HTTP request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	url := serverURL + "/mcp"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if verbose {
		logger.Debug("MCP response",
			"status", resp.Status,
			"response", string(responseBody))
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		logger.Error("failed to parse MCP response",
			"raw_response", string(responseBody),
			"error", err)
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for JSON-RPC error
	if jsonrpcError, ok := response["error"].(map[string]interface{}); ok {
		message, _ := jsonrpcError["message"].(string)
		return fmt.Errorf("MCP error: %s", message)
	}

	// Extract and display result
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