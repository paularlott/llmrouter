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

func (l *testLogger) Trace(msg string, args ...interface{})  {}
func (l *testLogger) Debug(msg string, args ...interface{})  {}
func (l *testLogger) Info(msg string, args ...interface{})   {}
func (l *testLogger) Warn(msg string, args ...interface{})   {}
func (l *testLogger) Error(msg string, args ...interface{})  {}
func (l *testLogger) Fatal(msg string, args ...interface{})  {}
func (l *testLogger) With(msg string, arg any) Logger        { return l }
func (l *testLogger) WithError(err error) Logger             { return l }
func (l *testLogger) WithGroup(group string) Logger          { return l }

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
