package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/lmchatkit"
	"github.com/paularlott/mcp"
)

// Elicitation: a tool that asks the person something, whether llmrouter's own
// script tool or a federated remote server's, reaches the chat (through
// lmchatkit's StandardHost) and the /mcp endpoint's external clients.

const confirmToolPy = `import scriptling.mcp.tool as tool
name = tool.get_string("name")
if not tool.confirm("Create the client " + name + "?"):
    tool.return_string("not created")
tool.return_string("created " + name)
`

const confirmToolToml = `description = "Creates a client after asking"
[[parameters]]
name = "name"
type = "string"
description = "Client name"
required = true
`

var approveSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"approve": map[string]any{"type": "boolean"}},
	"required":   []string{"approve"},
}

// elicitationRouter builds the MCP servers with one script tool, create_client,
// and one federated remote tool, fed__remote_delete, that both ask.
func elicitationRouter(t *testing.T) *MCPServer {
	t.Helper()
	remote := mcp.NewServer("remote", "1")
	remote.RegisterTool(mcp.NewTool("remote_delete", "deletes after asking"), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		res, err := mcp.Elicit(ctx, mcp.ElicitForm{Key: "ok", Message: "Delete it?", Schema: approveSchema})
		if err != nil {
			return nil, err
		}
		if res.Accepted() && res.Content["approve"] == true {
			return mcp.NewToolResponseText("deleted"), nil
		}
		return mcp.NewToolResponseText("kept"), nil
	})
	remoteSrv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	t.Cleanup(remoteSrv.Close)

	dir := t.TempDir()
	for name, body := range map[string]string{"create_client.toml": confirmToolToml, "create_client.py": confirmToolPy} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &types.Config{
		MCP:       types.MCPConfig{RemoteServers: []types.MCPRemoteServerConfig{{Namespace: "fed", URL: remoteSrv.URL, Federate: true}}},
		Scripting: types.ScriptingConfig{ToolsDir: dir},
	}
	s, err := NewMCPServer(cfg, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer: %v", err)
	}
	s.ReloadAllServers(nil)
	manager, err := NewScriptlingToolManager(cfg.Scripting, &testLogger{}, s.server, s.endpointServer)
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	t.Cleanup(manager.Shutdown)
	return s
}

func onlyKey(t *testing.T, need *lmchatkit.ToolInputRequired, message string) string {
	t.Helper()
	if need == nil || len(need.Requests) != 1 {
		t.Fatalf("want one question, got %+v", need)
	}
	for key, q := range need.Requests {
		if q.Message != message || q.Schema["type"] != "object" {
			t.Fatalf("asked %+v, want %q", q, message)
		}
		return key
	}
	return ""
}

func TestChatToolsAskThroughLmchatkit(t *testing.T) {
	s := elicitationRouter(t)
	host := &lmchatkit.StandardHost{MCPServer: func(context.Context) *mcp.Server { return s.server }}
	ctx := context.Background()

	for _, tc := range []struct {
		tool, args, message, yes, no string
	}{
		{"create_client", `{"name":"Acme"}`, "Create the client Acme?", "created Acme", "not created"},
		{"fed" + mcp.DefaultNamespaceSeparator + "remote_delete", `{}`, "Delete it?", "deleted", "kept"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			first, err := host.CallToolWithInput(ctx, tc.tool, json.RawMessage(tc.args), nil)
			if err != nil {
				t.Fatal(err)
			}
			key := onlyKey(t, first.InputRequired, tc.message)
			for answer, want := range map[string]string{"yes": tc.yes, "no": tc.no} {
				response := lmchatkit.ToolInputResponse{Action: "accept", Content: map[string]any{"approve": answer == "yes"}}
				res, err := host.CallToolWithInput(ctx, tc.tool, json.RawMessage(tc.args), &lmchatkit.ToolInput{
					Responses: map[string]lmchatkit.ToolInputResponse{key: response},
					State:     first.InputRequired.State,
				})
				if err != nil || res.InputRequired != nil || res.Content != want {
					t.Fatalf("answered %s: %+v %v", answer, res, err)
				}
			}
		})
	}
}

// An external 2026-07-28 client of the /mcp endpoint is asked the same way.
func TestEndpointToolsAskExternalClients(t *testing.T) {
	s := elicitationRouter(t)
	srv := httptest.NewServer(http.HandlerFunc(s.endpointServer.HandleRequest))
	t.Cleanup(srv.Close)

	post := func(params map[string]any) map[string]any {
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    mcp.MCPProtocolVersionModern,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{"elicitation": map[string]any{}},
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		req, _ := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", mcp.MCPProtocolVersionModern)
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", params["name"].(string))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	for _, tool := range []string{"create_client", "fed" + mcp.DefaultNamespaceSeparator + "remote_delete"} {
		t.Run(tool, func(t *testing.T) {
			args := map[string]any{"name": "Acme"}
			if tool != "create_client" {
				args = map[string]any{}
			}
			first, _ := post(map[string]any{"name": tool, "arguments": args})["result"].(map[string]any)
			requests, _ := first["inputRequests"].(map[string]any)
			if first["resultType"] != "input_required" || len(requests) != 1 {
				t.Fatalf("want a question, got %v", first)
			}
			var key string
			for k := range requests {
				key = k
			}
			done, _ := post(map[string]any{"name": tool, "arguments": args, "requestState": first["requestState"],
				"inputResponses": map[string]any{key: map[string]any{"action": "accept", "content": map[string]any{"approve": true}}}})["result"].(map[string]any)
			content, _ := done["content"].([]any)
			if len(content) == 0 {
				t.Fatalf("no result: %v", done)
			}
			if text := content[0].(map[string]any)["text"]; text != "created Acme" && text != "deleted" {
				t.Fatalf("result %v", done)
			}
		})
	}
}
