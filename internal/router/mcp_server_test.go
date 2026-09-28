package router

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
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
