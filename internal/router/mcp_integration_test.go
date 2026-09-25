package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
)

const testUIResourceURI = "ui://dashboard/dashboard.html"

// registerUITool adds a UI-linked tool + its paired ui:// resource to srv,
// mirroring what a real MCP Apps server (scriptling or otherwise) exposes.
func registerUITool(srv *mcp.Server) {
	srv.RegisterTool(
		mcp.NewTool("sales_report", "Get the sales report").
			UIResource(testUIResourceURI, "model", "app").
			Icons(mcp.Icon{Src: "https://example.com/icon.png", MimeType: "image/png"}),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseStructured(map[string]any{"records": []string{"a", "b"}}), nil
		},
	)
	srv.RegisterResource(
		mcp.NewResource(testUIResourceURI, "Sales Dashboard", "the dashboard ui", mcp.UIAppMimeType).
			UIMeta(mcp.UIResourceMeta{CSP: &mcp.UICSP{ResourceDomains: []string{"https://cdn.example.com"}}}),
		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
			return mcp.NewUIResourceResponseText(testUIResourceURI, "<html></html>", &mcp.UIResourceMeta{
				CSP: &mcp.UICSP{ResourceDomains: []string{"https://cdn.example.com"}},
			}), nil
		},
	)
}

// findToolByName pulls one tool's map out of a decoded tools/list result.
func findToolByName(t *testing.T, tools []interface{}, name string) map[string]interface{} {
	t.Helper()
	for _, raw := range tools {
		tm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if tm["name"] == name {
			return tm
		}
	}
	t.Fatalf("tool %q not found in tools/list result: %+v", name, tools)
	return nil
}

// assertUIToolMeta checks a decoded tools/list entry's _meta.ui and icons
// survived intact, regardless of whether the tool is served natively or
// federated from a remote MCP server.
func assertUIToolMeta(t *testing.T, tm map[string]interface{}) {
	t.Helper()
	meta, ok := tm["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _meta on tool, got: %+v", tm)
	}
	ui, ok := meta["ui"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _meta.ui on tool, got: %+v", meta)
	}
	if ui["resourceUri"] != testUIResourceURI {
		t.Errorf("resourceUri = %v, want %v", ui["resourceUri"], testUIResourceURI)
	}
	visibility, _ := ui["visibility"].([]interface{})
	if len(visibility) != 2 {
		t.Errorf("visibility = %v, want [model app]", ui["visibility"])
	}
	icons, ok := tm["icons"].([]interface{})
	if !ok || len(icons) != 1 {
		t.Fatalf("expected 1 icon on tool, got: %+v", tm["icons"])
	}
	icon, _ := icons[0].(map[string]interface{})
	if icon["src"] != "https://example.com/icon.png" {
		t.Errorf("icon src = %v, want https://example.com/icon.png", icon["src"])
	}
}

// TestMCPServer_ServesNativeUITool proves llmrouter's own /mcp endpoint
// serves _meta.ui/icons for a tool registered directly on it (the "serving"
// requirement), and that the paired ui:// resource resolves via
// resources/read with its own _meta.ui (CSP) hints intact.
func TestMCPServer_ServesNativeUITool(t *testing.T) {
	s := newTestMCPServer(t)
	// Native content is served on both servers (the scriptling manager
	// registers on each in production); mirror that here.
	registerUITool(s.server)
	registerUITool(s.endpointServer)

	resp := mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if resp["error"] != nil {
		t.Fatalf("tools/list returned error: %v", resp["error"])
	}
	result := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	assertUIToolMeta(t, findToolByName(t, tools, "sales_report"))

	resp = mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "resources/read",
		"params": map[string]interface{}{"uri": testUIResourceURI},
	})
	if resp["error"] != nil {
		t.Fatalf("resources/read returned error: %v", resp["error"])
	}
	result = resp["result"].(map[string]interface{})
	contents, _ := result["contents"].([]interface{})
	if len(contents) != 1 {
		t.Fatalf("expected 1 content entry, got: %+v", contents)
	}
	content := contents[0].(map[string]interface{})
	meta, ok := content["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _meta on resource content, got: %+v", content)
	}
	ui, ok := meta["ui"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _meta.ui on resource content, got: %+v", meta)
	}
	csp, ok := ui["csp"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected ui.csp, got: %+v", ui)
	}
	domains, _ := csp["resourceDomains"].([]interface{})
	if len(domains) != 1 || domains[0] != "https://cdn.example.com" {
		t.Errorf("resourceDomains = %v, want [https://cdn.example.com]", domains)
	}
}

// TestMCPServer_FederateGatingAndAppExclusion proves the /mcp endpoint's
// federation rules: by default a remote server's tools never reach the
// endpoint (the chat keeps them), and a server opted in with federate =
// true reaches it minus its MCP Apps tools — an app view speaks bare,
// host-agnostic tool names, so re-serving it under a federation namespace
// would break its in-page calls to its own tools.
func TestMCPServer_FederateGatingAndAppExclusion(t *testing.T) {
	newRemote := func() *httptest.Server {
		remote := mcp.NewServer("remote", "0.1.1")
		registerUITool(remote) // sales_report: the MCP Apps tool + its ui:// resource
		remote.RegisterTool(
			mcp.NewTool("plain_report", "a plain federated tool"),
			func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
				return mcp.NewToolResponseText("ok"), nil
			},
		)
		ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
		t.Cleanup(ts.Close)
		return ts
	}

	names := func(s *mcp.Server) string {
		var b strings.Builder
		for _, tool := range s.ListToolsWithContext(context.Background()) {
			b.WriteString(tool.Name + " ")
		}
		return b.String()
	}

	t.Run("federate opts in, minus apps", func(t *testing.T) {
		cfg := &types.Config{
			MCP: types.MCPConfig{
				RemoteServers: []types.MCPRemoteServerConfig{
					{Namespace: "fed", URL: newRemote().URL, Federate: true},
				},
			},
		}
		s, err := NewMCPServer(cfg, &testLogger{})
		if err != nil {
			t.Fatalf("NewMCPServer: %v", err)
		}
		s.ReloadAllServers(nil)

		endpoint := names(s.endpointServer)
		if !strings.Contains(endpoint, "fed"+mcp.DefaultNamespaceSeparator+"plain_report") {
			t.Fatalf("federated plain tool must reach the endpoint: %q", endpoint)
		}
		if strings.Contains(endpoint, "sales_report") {
			t.Fatalf("federated app tool must stay off the endpoint: %q", endpoint)
		}

		chat := names(s.server)
		if !strings.Contains(chat, "fed"+mcp.DefaultNamespaceSeparator+"sales_report") {
			t.Fatalf("chat-side server keeps the app tool (it mounts app views): %q", chat)
		}

		// The app's ui:// resource is readable through the chat-side server
		// (fan-out to the remote) but not through the endpoint.
		if _, err := s.server.ReadResource(context.Background(), testUIResourceURI); err != nil {
			t.Fatalf("chat-side read of the federated ui:// resource failed: %v", err)
		}
		if _, err := s.endpointServer.ReadResource(context.Background(), testUIResourceURI); err == nil {
			t.Fatal("endpoint must not serve a federated app's ui:// resource")
		}
	})

	t.Run("no federate means endpoint-native only", func(t *testing.T) {
		cfg := &types.Config{
			MCP: types.MCPConfig{
				RemoteServers: []types.MCPRemoteServerConfig{
					{Namespace: "fed", URL: newRemote().URL},
				},
			},
		}
		s, err := NewMCPServer(cfg, &testLogger{})
		if err != nil {
			t.Fatalf("NewMCPServer: %v", err)
		}
		s.ReloadAllServers(nil)

		if endpoint := names(s.endpointServer); strings.Contains(endpoint, "fed") {
			t.Fatalf("a server without federate must not reach the endpoint: %q", endpoint)
		}
		if chat := names(s.server); !strings.Contains(chat, "fed"+mcp.DefaultNamespaceSeparator+"plain_report") {
			t.Fatalf("chat-side server keeps every remote server: %q", chat)
		}
	})
}

// TestMCPServer_DeclaresUIAppsSupportToRemote is the other direction of the
// federation contract TestMCPServer_PassesThroughFederatedUITool proves:
// not just "does a remote's _meta.ui survive federation", but "does the
// remote even know it's allowed to attach one in the first place". A
// spec-conformant remote server may only attach _meta.ui for a client that
// declared capabilities.extensions[io.modelcontextprotocol/ui] — without
// declareUIAppsSupport, this router's own federation client never does, so
// such a server would silently serve every tool as plain text and MCP Apps
// would never work against it, with no error anywhere to explain why.
func TestMCPServer_DeclaresUIAppsSupportToRemote(t *testing.T) {
	remote := mcp.NewServer("remote", "0.0.1")
	registerUITool(remote)
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "", URL: ts.URL},
			},
		},
	}
	s, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	s.ReloadAllServers(nil)

	// Force the federation client to actually connect (Initialize may be
	// lazy) before checking what it declared.
	resp := mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if resp["error"] != nil {
		t.Fatalf("tools/list returned error: %v", resp["error"])
	}

	mimeTypes, ok := mcp.SupportsUIApps(remote.ClientCapabilities())
	if !ok {
		t.Fatalf("expected the remote server to observe this router declaring MCP Apps support, capabilities = %v", remote.ClientCapabilities())
	}
	if len(mimeTypes) != 1 || mimeTypes[0] != mcp.UIAppMimeType {
		t.Errorf("mimeTypes = %v, want [%q]", mimeTypes, mcp.UIAppMimeType)
	}
}

// TestGetToolsForAdmin_IconsAndAppFlag proves the "MCP Servers" admin page's
// tool listing surfaces a federated tool's icons and whether it's an MCP
// Apps tool (has a linked ui:// resource) — sales_report has both an icon
// and a resourceUri, add_sale has neither, and the admin listing must not
// invent one for it.
func TestGetToolsForAdmin_IconsAndAppFlag(t *testing.T) {
	remote := mcp.NewServer("remote", "0.0.1")
	registerUITool(remote)
	// A plain "app-only action" tool (like the dashboard's real add_sale):
	// visibility-restricted but no resourceUri of its own and no icon — the
	// admin listing must not mark it as an app or invent an icon for it.
	remote.RegisterTool(
		mcp.NewTool("add_sale", "Add a sale record").Visibility("app"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("ok"), nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "dashboard", URL: ts.URL},
			},
		},
	}
	s, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}

	tools, err := s.GetToolsForAdmin("dashboard")
	if err != nil {
		t.Fatalf("GetToolsForAdmin: %v", err)
	}

	var salesReport, addSale *admin.ToolInfo
	for i := range tools {
		switch tools[i].Name {
		case "sales_report":
			salesReport = &tools[i]
		case "add_sale":
			addSale = &tools[i]
		}
	}
	if salesReport == nil || addSale == nil {
		t.Fatalf("expected both sales_report and add_sale in admin tool list, got: %+v", tools)
	}

	if !salesReport.IsApp {
		t.Error("expected sales_report.IsApp = true (it has a linked ui:// resource)")
	}
	if len(salesReport.Icons) != 1 || salesReport.Icons[0].Src != "https://example.com/icon.png" {
		t.Errorf("expected sales_report to carry its icon, got: %+v", salesReport.Icons)
	}

	if addSale.IsApp {
		t.Error("expected add_sale.IsApp = false (visibility-only, no resourceUri)")
	}
	if len(addSale.Icons) != 0 {
		t.Errorf("expected add_sale to have no icons, got: %+v", addSale.Icons)
	}
}

// TestGetProtocolVersionForAdmin proves the "MCP Servers" admin page can
// surface which protocol version a federated remote server actually
// negotiated — captured via mcp.Client.ProtocolVersion(), which this session
// added specifically so this couldn't previously be observed at all.
func TestGetProtocolVersionForAdmin(t *testing.T) {
	remote := mcp.NewServer("remote", "0.0.1")
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := &types.Config{
		MCP: types.MCPConfig{
			RemoteServers: []types.MCPRemoteServerConfig{
				{Namespace: "dashboard", URL: ts.URL},
			},
		},
	}
	s, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}

	version, err := s.GetProtocolVersionForAdmin("dashboard")
	if err != nil {
		t.Fatalf("GetProtocolVersionForAdmin: %v", err)
	}
	if version != mcp.MCPProtocolVersionModern {
		t.Errorf("version = %q, want %q (this remote supports server/discover)", version, mcp.MCPProtocolVersionModern)
	}

	// An unknown namespace returns "" without error, not a lookup failure —
	// the admin UI treats this as "no badge", not an error state.
	version, err = s.GetProtocolVersionForAdmin("does-not-exist")
	if err != nil {
		t.Fatalf("GetProtocolVersionForAdmin(unknown): %v", err)
	}
	if version != "" {
		t.Errorf("version for unknown namespace = %q, want empty", version)
	}
}
