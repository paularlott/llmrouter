//go:build !server

package cmd

import (
	"context"

	"github.com/paularlott/cli"
	"github.com/paularlott/llmrouter/internal/desktop"
)

// In desktop builds, `llmrouter` with no subcommand opens the GUI window:
// the in-process HTTP server plus a webview pointing at it.
// In server builds (-tags server), RootCmd.Run stays nil and the CLI library
// shows help — the original behaviour before desktop support was added.
func init() {
	RootCmd.Run = func(ctx context.Context, cmd *cli.Command) error {
		return desktop.Run(ctx, cmd)
	}
}
