package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/paularlott/cli"
)

var ModelsCmd = &cli.Command{
	Name:        "models",
	Usage:       "List available models",
	Description: "List all models available from the LLM router",
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
		serverURL := cmd.GetString("server")
		token := cmd.GetString("token")

		client := &http.Client{Timeout: 30 * time.Second}
		req, err := http.NewRequest("GET", serverURL+"/v1/models", nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		var out any
		if err := json.Unmarshal(body, &out); err != nil {
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
