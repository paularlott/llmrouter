package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/paularlott/cli"
)

var AskCmd = &cli.Command{
	Name:        "ask",
	Usage:       "Ask a model a question",
	Description: "Send a question to a specific model and display the JSON response",
	Arguments: []cli.Argument{
		&cli.StringArg{
			Name:     "model",
			Required: true,
			Usage:    "Model to use",
		},
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:         "server",
			Usage:        "Server URL",
			DefaultValue: "http://localhost:12345",
		},
		&cli.StringFlag{
			Name:    "token",
			Aliases: []string{"t"},
			Usage:   "Bearer token for server authentication",
		},
	},
	Run: func(ctx context.Context, cmd *cli.Command) error {
		model := cmd.GetStringArg("model")
		question := strings.Join(cmd.GetArgs(), " ")
		serverURL := cmd.GetString("server")
		token := cmd.GetString("token")

		if question == "" {
			return fmt.Errorf("question is required")
		}

		payload := map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": question},
			},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}

		client := &http.Client{Timeout: 120 * time.Second}
		req, err := http.NewRequest("POST", serverURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		var out any
		if err := json.Unmarshal(respBody, &out); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		formatted, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format response: %w", err)
		}
		fmt.Println(string(formatted))
		return nil
	},
}
