package router

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp"
)

// testLogger implements Logger for testing
type testLogger struct{}

func (l *testLogger) Trace(msg string, args ...interface{}) {}
func (l *testLogger) Debug(msg string, args ...interface{}) {}
func (l *testLogger) Info(msg string, args ...interface{})  {}
func (l *testLogger) Warn(msg string, args ...interface{})  {}
func (l *testLogger) Error(msg string, args ...interface{}) {}
func (l *testLogger) Fatal(msg string, args ...interface{}) {}
func (l *testLogger) With(msg string, arg any) Logger       { return l }
func (l *testLogger) WithError(err error) Logger            { return l }
func (l *testLogger) WithGroup(group string) Logger         { return l }

func newTestMCPServer(t *testing.T) *MCPServer {
	t.Helper()
	s, err := NewMCPServer(&types.Config{}, &testLogger{})
	if err != nil {
		t.Fatalf("NewMCPServer failed: %v", err)
	}
	return s
}

func mcpRequest(t *testing.T, s *MCPServer, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.HandleRequest(w, req)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	return resp
}

func TestMCPServerInitialize(t *testing.T) {
	s := newTestMCPServer(t)
	resp := mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
		},
	})
	if resp["error"] != nil {
		t.Fatalf("initialize returned error: %v", resp["error"])
	}
	if resp["result"] == nil {
		t.Fatal("initialize returned no result")
	}
}

func TestMCPServerToolsList(t *testing.T) {
	s := newTestMCPServer(t)
	resp := mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	if resp["error"] != nil {
		t.Fatalf("tools/list returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatal("tools/list returned no result")
	}
	// With no remote servers configured, tools list should be empty or contain only builtins
	tools, _ := result["tools"].([]interface{})
	t.Logf("tools/list returned %d tools", len(tools))
}

// TestMCPServerSupportsModernProtocol proves llmrouter's own /mcp endpoint
// serves the MCP spec's Modern (2026-07-28+, stateless per-request) protocol
// automatically, since MCPServer.HandleRequest is a pure passthrough to the
// underlying *mcp.Server — the library's Dual-era support applies to any
// server built from it with zero llmrouter-specific wiring. A Modern request
// is marked by the _meta.io.modelcontextprotocol/protocolVersion field (see
// isModernRequest in the mcp package); server/discover is the Modern
// handshake's own discovery call and only exists in that era.
func TestMCPServerSupportsModernProtocol(t *testing.T) {
	s := newTestMCPServer(t)
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientInfo":         map[string]interface{}{"name": "test-client", "version": "1.0"},
				"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
			},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	// Modern requests must mirror their JSON-RPC method and protocol version
	// in these headers (the spec's "header mismatch" validation) —
	// server/discover is no exception.
	req.Header.Set("Mcp-Method", "server/discover")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	w := httptest.NewRecorder()
	s.HandleRequest(w, req)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["error"] != nil {
		t.Fatalf("server/discover returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatal("server/discover returned no result")
	}
	versions, _ := result["supportedVersions"].([]interface{})
	if len(versions) == 0 {
		t.Fatalf("expected supportedVersions in a Modern DiscoverResult, got: %+v", result)
	}
}

// list_skills and get_skill are registered on the public /mcp endpoint
// (s.endpointServer, driven here via s.HandleRequest) as a stopgap for MCP
// clients that don't implement the skills extension client-side — but must
// never appear on the chat-side server (s.server), which lmchatkit already
// serves get_skill for internally.
func TestMCPServerExposesSkillToolsOnPublicEndpointOnly(t *testing.T) {
	s := newTestMCPServer(t)
	if err := s.endpointServer.RegisterSkill(mcp.NewSkill("code-review").
		Description("How to review changes").
		File("SKILL.md", []byte("---\nname: code-review\ndescription: How to review changes\n---\nRead the diff twice."))); err != nil {
		t.Fatalf("RegisterSkill: %v", err)
	}

	// tools/list on the public endpoint includes both.
	resp := mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	var names []string
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		names = append(names, tool["name"].(string))
	}
	if !containsStr(names, "get_skill") || !containsStr(names, "list_skills") {
		t.Fatalf("public /mcp tools/list = %v, want get_skill and list_skills", names)
	}

	// list_skills surfaces the skill just registered.
	resp = mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{"name": "list_skills", "arguments": map[string]interface{}{}},
	})
	if resp["error"] != nil {
		t.Fatalf("tools/call list_skills error: %v", resp["error"])
	}
	listText := toolResultText(t, resp)
	if !bytes.Contains([]byte(listText), []byte(`"name":"code-review"`)) ||
		!bytes.Contains([]byte(listText), []byte(`"uri":"skill://code-review/SKILL.md"`)) {
		t.Fatalf("list_skills content = %s, want the registered skill's name/uri", listText)
	}

	// get_skill retrieves its full content by URI.
	resp = mcpRequest(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]interface{}{"name": "get_skill", "arguments": map[string]interface{}{"uri": "skill://code-review/SKILL.md"}},
	})
	if resp["error"] != nil {
		t.Fatalf("tools/call get_skill error: %v", resp["error"])
	}
	getText := toolResultText(t, resp)
	if !bytes.Contains([]byte(getText), []byte("Read the diff twice.")) {
		t.Fatalf("get_skill content = %q, want the skill's instructions", getText)
	}

	// The chat-side server never gets these tools: lmchatkit's own
	// lmchatkit__get_skill already covers the chat, and list_skills has no
	// chat-side equivalent at all.
	req := httptest.NewRequest("POST", "/chat-mcp", bytes.NewReader(mustJSON(t, map[string]interface{}{
		"jsonrpc": "2.0", "id": 4, "method": "tools/list",
	})))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.HandleRequest(w, req)
	var chatResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&chatResp)
	chatResult, _ := chatResp["result"].(map[string]interface{})
	chatTools, _ := chatResult["tools"].([]interface{})
	for _, raw := range chatTools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		if name == "get_skill" || name == "list_skills" {
			t.Fatalf("chat-side server must not expose %q", name)
		}
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func toolResultText(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	result, _ := resp["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("tool result has no content: %+v", resp)
	}
	block, _ := content[0].(map[string]interface{})
	text, _ := block["text"].(string)
	return text
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
