package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"

	mcplib "github.com/paularlott/mcp"
)

// The system-prompt skills listing must include skills from attached remote
// MCP servers (namespaced title), not just local ones — a regression test
// for the chat silently missing remote skills.
func TestAugmentIncludesRemoteSkills(t *testing.T) {
	remote := mcplib.NewServer("scriptling-remote", "1.0")
	remote.RegisterSkill(mcplib.NewSkill("dashboard-ops").
		Description("Operating the sales dashboard").
		File("SKILL.md", []byte("drive the dashboard")))
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "app", URL: ts.URL, Token: "t"},
			},
		},
	}
	mcpServer, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	mcpServer.ReloadAllServers(nil)

	r := &Router{config: cfg, mcpServer: mcpServer}
	out := r.listChatSkills(context.Background())
	if !strings.Contains(out, "app/dashboard-ops: Operating the sales dashboard (skill://dashboard-ops/SKILL.md)") {
		t.Fatalf("remote skill missing from prompt listing:\n%s", out)
	}
}
