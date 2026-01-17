package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestMCPServerIntegration tests the full MCP server with native mode
func TestMCPServerIntegration(t *testing.T) {
	tempDir := t.TempDir()

	// Create a tool with keywords
	tool1Dir := filepath.Join(tempDir, "tool1")
	os.MkdirAll(tool1Dir, 0755)
	tool1TOML := []byte(`
name = "tool1"
description = "A tool with keywords"
keywords = ["search", "test"]
script = "script.py"
`)
	os.WriteFile(filepath.Join(tool1Dir, "tool.toml"), tool1TOML, 0644)
	os.WriteFile(filepath.Join(tool1Dir, "script.py"), []byte("print('ok')"), 0644)

	// Create a tool without keywords
	tool2Dir := filepath.Join(tempDir, "tool2")
	os.MkdirAll(tool2Dir, 0755)
	tool2TOML := []byte(`
name = "tool2"
description = "A tool without keywords"
script = "script.py"
`)
	os.WriteFile(filepath.Join(tool2Dir, "tool.toml"), tool2TOML, 0644)
	os.WriteFile(filepath.Join(tool2Dir, "script.py"), []byte("print('ok')"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	logger := &testLogger{}
	router := &Router{} // Mock router

	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Test tools/list in native mode
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mcpServer.HandleRequest(w, req)

	var response struct {
		Result struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	toolNames := make(map[string]bool)
	for _, tool := range response.Result.Tools {
		if name, ok := tool["name"].(string); ok {
			toolNames[name] = true
			t.Logf("Found tool: %s", name)
		}
	}

	// In native mode, we should see:
	// - execute_code (builtin)
	// - tool1 (from provider)
	// - tool2 (from provider)

	if !toolNames["execute_code"] {
		t.Error("execute_code should be visible")
	}
	if !toolNames["tool1"] {
		t.Error("tool1 should be visible in native mode")
	}
	if !toolNames["tool2"] {
		t.Error("tool2 should be visible in native mode")
	}

	t.Logf("Total tools visible: %d", len(toolNames))
}

// TestMCPServerNativeAndOnDemandTools tests mixed visibility tools
func TestMCPServerNativeAndOnDemandTools(t *testing.T) {
	tempDir := t.TempDir()

	// Create a native tool (default visibility)
	nativeDir := filepath.Join(tempDir, "native_tool")
	os.MkdirAll(nativeDir, 0755)
	nativeTOML := []byte(`
name = "native_tool"
description = "A native tool visible in tools/list"
keywords = ["native", "visible"]
script = "script.py"
visibility = "native"
`)
	os.WriteFile(filepath.Join(nativeDir, "tool.toml"), nativeTOML, 0644)
	os.WriteFile(filepath.Join(nativeDir, "script.py"), []byte("print('native')"), 0644)

	// Create an ondemand tool (hidden from list, searchable)
	ondemandDir := filepath.Join(tempDir, "ondemand_tool")
	os.MkdirAll(ondemandDir, 0755)
	ondemandTOML := []byte(`
name = "ondemand_tool"
description = "An ondemand tool only via search"
keywords = ["ondemand", "hidden", "search"]
script = "script.py"
visibility = "ondemand"
`)
	os.WriteFile(filepath.Join(ondemandDir, "tool.toml"), ondemandTOML, 0644)
	os.WriteFile(filepath.Join(ondemandDir, "script.py"), []byte("print('ondemand')"), 0644)

	// Create another native tool without explicit visibility (default to native)
	defaultDir := filepath.Join(tempDir, "default_tool")
	os.MkdirAll(defaultDir, 0755)
	defaultTOML := []byte(`
name = "default_tool"
description = "A tool with default visibility (native)"
script = "script.py"
`)
	os.WriteFile(filepath.Join(defaultDir, "tool.toml"), defaultTOML, 0644)
	os.WriteFile(filepath.Join(defaultDir, "script.py"), []byte("print('default')"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	logger := &testLogger{}
	router := &Router{}

	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Test tools/list in native mode
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mcpServer.HandleRequest(w, req)

	var response struct {
		Result struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	toolNames := make(map[string]bool)
	for _, tool := range response.Result.Tools {
		if name, ok := tool["name"].(string); ok {
			toolNames[name] = true
			t.Logf("Found tool in native mode: %s", name)
		}
	}

	// In native mode:
	// - execute_code (builtin) should be visible
	// - native_tool should be visible
	// - default_tool should be visible (default visibility = native)
	// - ondemand_tool should NOT be visible (but searchable)
	// - tool_search and execute_tool should be visible (because we have ondemand tools)

	if !toolNames["execute_code"] {
		t.Error("execute_code should be visible")
	}
	if !toolNames["native_tool"] {
		t.Error("native_tool should be visible in native mode")
	}
	if !toolNames["default_tool"] {
		t.Error("default_tool should be visible (defaults to native)")
	}
	if toolNames["ondemand_tool"] {
		t.Error("ondemand_tool should NOT be visible in tools/list")
	}
	if !toolNames["tool_search"] {
		t.Error("tool_search should be visible when there are ondemand tools")
	}
	if !toolNames["execute_tool"] {
		t.Error("execute_tool should be visible when there are ondemand tools")
	}

	t.Logf("Total tools visible in native mode: %d", len(toolNames))

	// Now test tool_search to find the ondemand tool
	searchBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "tool_search",
			"arguments": map[string]interface{}{
				"query": "ondemand",
			},
		},
	}
	body, _ = json.Marshal(searchBody)

	req = httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	mcpServer.HandleRequest(w, req)

	var searchResponse struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&searchResponse)

	// Check that ondemand_tool is found in search results
	if len(searchResponse.Result.Content) > 0 {
		searchText := searchResponse.Result.Content[0].Text
		t.Logf("Search result: %s", searchText)

		var searchResults []map[string]interface{}
		if err := json.Unmarshal([]byte(searchText), &searchResults); err != nil {
			t.Logf("Search result is not JSON array, checking as text: %s", searchText)
		} else {
			foundOnDemand := false
			for _, result := range searchResults {
				if name, ok := result["name"].(string); ok && name == "ondemand_tool" {
					foundOnDemand = true
					t.Logf("Found ondemand_tool in search results")
				}
			}
			if !foundOnDemand {
				t.Error("ondemand_tool should be found via tool_search")
			}
		}
	}
}

// TestMCPServerDiscoveryMode tests the /mcp/discovery endpoint
func TestMCPServerDiscoveryMode(t *testing.T) {
	tempDir := t.TempDir()

	// Create a native tool
	nativeDir := filepath.Join(tempDir, "native_tool")
	os.MkdirAll(nativeDir, 0755)
	nativeTOML := []byte(`
name = "native_tool"
description = "A native tool"
keywords = ["native"]
script = "script.py"
`)
	os.WriteFile(filepath.Join(nativeDir, "tool.toml"), nativeTOML, 0644)
	os.WriteFile(filepath.Join(nativeDir, "script.py"), []byte("print('native')"), 0644)

	// Create an ondemand tool
	ondemandDir := filepath.Join(tempDir, "ondemand_tool")
	os.MkdirAll(ondemandDir, 0755)
	ondemandTOML := []byte(`
name = "ondemand_tool"
description = "An ondemand tool"
keywords = ["ondemand"]
script = "script.py"
visibility = "ondemand"
`)
	os.WriteFile(filepath.Join(ondemandDir, "tool.toml"), ondemandTOML, 0644)
	os.WriteFile(filepath.Join(ondemandDir, "script.py"), []byte("print('ondemand')"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	logger := &testLogger{}
	router := &Router{}

	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Test tools/list in discovery mode
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/mcp/discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mcpServer.HandleDiscoveryRequest(w, req)

	var response struct {
		Result struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	toolNames := make(map[string]bool)
	for _, tool := range response.Result.Tools {
		if name, ok := tool["name"].(string); ok {
			toolNames[name] = true
			t.Logf("Found tool in discovery mode: %s", name)
		}
	}

	// In discovery mode (force ondemand):
	// - Only tool_search and execute_tool should be visible
	// - All other tools are hidden but searchable

	if !toolNames["tool_search"] {
		t.Error("tool_search should be visible in discovery mode")
	}
	if !toolNames["execute_tool"] {
		t.Error("execute_tool should be visible in discovery mode")
	}
	if toolNames["native_tool"] {
		t.Error("native_tool should NOT be visible in discovery mode")
	}
	if toolNames["ondemand_tool"] {
		t.Error("ondemand_tool should NOT be visible in discovery mode")
	}
	if toolNames["execute_code"] {
		t.Error("execute_code should NOT be visible in discovery mode")
	}

	if len(toolNames) != 2 {
		t.Errorf("Expected exactly 2 tools in discovery mode, got %d", len(toolNames))
	}

	// Test that tools are searchable via tool_search
	searchBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "tool_search",
			"arguments": map[string]interface{}{
				"query": "", // Empty query lists all
			},
		},
	}
	body, _ = json.Marshal(searchBody)

	req = httptest.NewRequest("POST", "/mcp/discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	mcpServer.HandleDiscoveryRequest(w, req)

	var searchResponse struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&searchResponse)

	if len(searchResponse.Result.Content) > 0 {
		searchText := searchResponse.Result.Content[0].Text
		t.Logf("Discovery search result: %s", searchText)

		var searchResults []map[string]interface{}
		if err := json.Unmarshal([]byte(searchText), &searchResults); err == nil {
			foundNative := false
			foundOnDemand := false
			foundExecuteCode := false
			for _, result := range searchResults {
				name, _ := result["name"].(string)
				if name == "native_tool" {
					foundNative = true
				}
				if name == "ondemand_tool" {
					foundOnDemand = true
				}
				if name == "execute_code" {
					foundExecuteCode = true
				}
			}
			if !foundNative {
				t.Error("native_tool should be searchable in discovery mode")
			}
			if !foundOnDemand {
				t.Error("ondemand_tool should be searchable in discovery mode")
			}
			if !foundExecuteCode {
				t.Error("execute_code should be searchable in discovery mode")
			}
		}
	}
}
// TestMCPServerNoDiscoveryToolsWithoutOnDemand tests that discovery tools
// are NOT shown when there are no ondemand tools
func TestMCPServerNoDiscoveryToolsWithoutOnDemand(t *testing.T) {
	tempDir := t.TempDir()

	// Create ONLY native tools (no ondemand tools)
	nativeDir := filepath.Join(tempDir, "native_tool")
	os.MkdirAll(nativeDir, 0755)
	nativeTOML := []byte(`
name = "native_tool"
description = "A native tool"
keywords = ["native"]
script = "script.py"
visibility = "native"
`)
	os.WriteFile(filepath.Join(nativeDir, "tool.toml"), nativeTOML, 0644)
	os.WriteFile(filepath.Join(nativeDir, "script.py"), []byte("print('native')"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	logger := &testLogger{}
	router := &Router{}

	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Test tools/list - should NOT show discovery tools
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mcpServer.HandleRequest(w, req)

	var response struct {
		Result struct {
			Tools []map[string]interface{} `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&response)

	toolNames := make(map[string]bool)
	for _, tool := range response.Result.Tools {
		if name, ok := tool["name"].(string); ok {
			toolNames[name] = true
			t.Logf("Found tool: %s", name)
		}
	}

	// Without ondemand tools:
	// - execute_code should be visible
	// - native_tool should be visible
	// - tool_search and execute_tool should NOT be visible

	if !toolNames["execute_code"] {
		t.Error("execute_code should be visible")
	}
	if !toolNames["native_tool"] {
		t.Error("native_tool should be visible")
	}
	if toolNames["tool_search"] {
		t.Error("tool_search should NOT be visible when no ondemand tools exist")
	}
	if toolNames["execute_tool"] {
		t.Error("execute_tool should NOT be visible when no ondemand tools exist")
	}
}

// TestMCPServerSearchOnlyOnDemandInNormalMode tests that tool_search only
// searches ondemand tools in normal mode (not native tools)
func TestMCPServerSearchOnlyOnDemandInNormalMode(t *testing.T) {
	tempDir := t.TempDir()

	// Create a native tool
	nativeDir := filepath.Join(tempDir, "native_tool")
	os.MkdirAll(nativeDir, 0755)
	nativeTOML := []byte(`
name = "native_tool"
description = "A native tool with searchable keywords"
keywords = ["searchme", "findme"]
script = "script.py"
visibility = "native"
`)
	os.WriteFile(filepath.Join(nativeDir, "tool.toml"), nativeTOML, 0644)
	os.WriteFile(filepath.Join(nativeDir, "script.py"), []byte("print('native')"), 0644)

	// Create an ondemand tool
	ondemandDir := filepath.Join(tempDir, "ondemand_tool")
	os.MkdirAll(ondemandDir, 0755)
	ondemandTOML := []byte(`
name = "ondemand_tool"
description = "An ondemand tool"
keywords = ["searchme", "findme"]
script = "script.py"
visibility = "ondemand"
`)
	os.WriteFile(filepath.Join(ondemandDir, "tool.toml"), ondemandTOML, 0644)
	os.WriteFile(filepath.Join(ondemandDir, "script.py"), []byte("print('ondemand')"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	logger := &testLogger{}
	router := &Router{}

	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Search for "searchme" in normal mode - should ONLY find ondemand_tool
	searchBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "tool_search",
			"arguments": map[string]interface{}{
				"query": "searchme",
			},
		},
	}
	body, _ := json.Marshal(searchBody)

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mcpServer.HandleRequest(w, req)

	var searchResponse struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&searchResponse)

	if len(searchResponse.Result.Content) > 0 {
		searchText := searchResponse.Result.Content[0].Text
		t.Logf("Search result in normal mode: %s", searchText)

		var searchResults []map[string]interface{}
		if err := json.Unmarshal([]byte(searchText), &searchResults); err == nil {
			foundNative := false
			foundOnDemand := false
			for _, result := range searchResults {
				name, _ := result["name"].(string)
				if name == "native_tool" {
					foundNative = true
				}
				if name == "ondemand_tool" {
					foundOnDemand = true
				}
			}
			if foundNative {
				t.Error("native_tool should NOT be searchable in normal mode (only in discovery mode)")
			}
			if !foundOnDemand {
				t.Error("ondemand_tool should be searchable in normal mode")
			}
		}
	}
}