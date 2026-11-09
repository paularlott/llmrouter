package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
)

// A tool's title and behaviour hints (readOnlyHint, destructiveHint, ...)
// reach both of the router's MCP servers intact: for a federated remote
// tool, and for the router's own tools after the tool list is rebuilt (what
// a remote server's tools/list_changed triggers). Clients decide from these
// hints whether to ask before a call; the mcp library once dropped both when
// it rebuilt the list.
func TestMCPServer_KeepsToolTitleAndHints(t *testing.T) {
	ok := func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("ok"), nil
	}
	remote := mcp.NewServer("remote", "0.1.1")
	remote.RegisterTool(mcp.NewTool("delete_note", "Delete a note").Title("Delete Note").Destructive(true).Idempotent(), ok)
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	t.Cleanup(ts.Close)

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "fed", URL: ts.URL, Federate: true},
			},
		},
	}
	s, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	// One of the router's own tools, registered on both servers as
	// scriptlingToolManager.registerTool does.
	own := mcp.NewTool("search_notes", "Search notes").Title("Search Notes").ReadOnly().OpenWorld(false)
	for _, srv := range []*mcp.Server{s.server, s.endpointServer} {
		srv.RegisterTool(own, ok)
	}
	s.ReloadAllServers(nil)

	yes, no := true, false
	federated := &mcp.ToolAnnotations{DestructiveHint: &yes, IdempotentHint: &yes}
	native := &mcp.ToolAnnotations{ReadOnlyHint: &yes, OpenWorldHint: &no}

	for name, srv := range map[string]*mcp.Server{"chat": s.server, "endpoint": s.endpointServer} {
		if err := srv.RefreshTools(context.Background()); err != nil {
			t.Fatalf("%s: RefreshTools: %v", name, err)
		}
		tools := map[string]mcp.MCPTool{}
		for _, tool := range srv.ListToolsWithContext(context.Background()) {
			tools[tool.Name] = tool
		}
		for toolName, want := range map[string]struct {
			title string
			hints *mcp.ToolAnnotations
		}{
			"fed" + mcp.DefaultNamespaceSeparator + "delete_note": {"Delete Note", federated},
			"search_notes": {"Search Notes", native},
		} {
			tool, found := tools[toolName]
			if !found {
				t.Fatalf("%s: %s not listed", name, toolName)
			}
			if tool.Title != want.title {
				t.Errorf("%s: %s title = %q, want %q", name, toolName, tool.Title, want.title)
			}
			if !reflect.DeepEqual(tool.Annotations, want.hints) {
				t.Errorf("%s: %s annotations = %+v, want %+v", name, toolName, tool.Annotations, want.hints)
			}
		}
	}
}
