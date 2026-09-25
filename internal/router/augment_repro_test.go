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

// A remote marked federate serves its skills through the public /mcp
// endpoint under skill://<namespace>/… URIs; without the flag the endpoint
// lists none (the web chat's prompt listing is the only path then).
func TestEndpointFederatesSkillsWhenMarked(t *testing.T) {
	remote := mcplib.NewServer("scriptling-remote", "1.0")
	remote.RegisterSkill(mcplib.NewSkill("dashboard-ops").
		Description("Operating the sales dashboard").
		File("SKILL.md", []byte("drive the dashboard")))
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "app", URL: ts.URL, Token: "t", Federate: true},
			},
		},
	}
	mcpServer, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	mcpServer.ReloadAllServers(nil)

	skills := mcpServer.endpointServer.ListSkillsWithContext(context.Background())
	if len(skills) != 1 || skills[0].URI != "skill://app/dashboard-ops/SKILL.md" {
		t.Fatalf("endpoint skills/list = %+v, want the namespaced federated skill", skills)
	}
	if _, ok := mcpServer.endpointServer.GetSkillWithContext(context.Background(), "skill://app/dashboard-ops/SKILL.md"); !ok {
		t.Fatal("endpoint skills/get must resolve the federated URI")
	}
	resp, err := mcpServer.endpointServer.ReadResource(context.Background(), "skill://app/dashboard-ops/SKILL.md")
	if err != nil || !strings.Contains(resp.Contents[0].Text, "drive the dashboard") {
		t.Fatalf("endpoint resources/read of federated skill = (%+v, %v)", resp, err)
	}

	// The chat-side server does not federate skills through registration:
	// the prompt listing is its only skills surface for remotes.
	if skills := mcpServer.server.ListSkillsWithContext(context.Background()); len(skills) != 0 {
		t.Fatalf("chat-side server must not list federated remote skills: %+v", skills)
	}

	// Without federate, the endpoint serves nothing from the remote.
	cfgPlain := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "app", URL: ts.URL, Token: "t"},
			},
		},
	}
	plainServer, err := NewMCPServer(cfgPlain, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	plainServer.ReloadAllServers(nil)
	if skills := plainServer.endpointServer.ListSkillsWithContext(context.Background()); len(skills) != 0 {
		t.Fatalf("non-federated remote must not contribute endpoint skills: %+v", skills)
	}
}
