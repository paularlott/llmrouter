package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

// TestScriptToolProviderBasic tests basic tool loading
func TestScriptToolProviderBasic(t *testing.T) {
	tempDir := t.TempDir()

	// Create tool 1
	tool1Dir := filepath.Join(tempDir, "tool1")
	os.MkdirAll(tool1Dir, 0755)
	tool1TOML := []byte(`
name = "tool1"
description = "First test tool"
keywords = ["test", "first"]
script = "script.py"

[parameters.input]
type = "string"
description = "Input parameter"
required = true
`)
	os.WriteFile(filepath.Join(tool1Dir, "tool.toml"), tool1TOML, 0644)
	os.WriteFile(filepath.Join(tool1Dir, "script.py"), []byte("import llmr.mcp\ndef main():\n    llmr.mcp.return_string('tool1')\n"), 0644)

	// Create tool 2
	tool2Dir := filepath.Join(tempDir, "tool2")
	os.MkdirAll(tool2Dir, 0755)
	tool2TOML := []byte(`
name = "tool2"
description = "Second test tool"
keywords = ["test", "second"]
script = "script.py"

[parameters.input]
type = "string"
description = "Input parameter"
required = true
`)
	os.WriteFile(filepath.Join(tool2Dir, "tool.toml"), tool2TOML, 0644)
	os.WriteFile(filepath.Join(tool2Dir, "script.py"), []byte("import llmr.mcp\ndef main():\n    llmr.mcp.return_string('tool2')\n"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	mcpServer := &MCPServer{
		config:    config,
		logger:    &testLogger{},
		toolsPath: tempDir,
	}

	provider := NewScriptToolProvider(mcpServer)
	tools, err := provider.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools failed: %v", err)
	}

	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["tool1"] {
		t.Error("tool1 should be returned")
	}
	if !toolNames["tool2"] {
		t.Error("tool2 should be returned")
	}
}

// TestDynamicToolLoading tests that tools can be added/removed/modified without restart
func TestDynamicToolLoading(t *testing.T) {
	tempDir := t.TempDir()

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	mcpServer := &MCPServer{
		config:    config,
		logger:    &testLogger{},
		toolsPath: tempDir,
	}

	provider := NewScriptToolProvider(mcpServer)

	// Initially no tools
	tools, _ := provider.GetTools(context.Background())
	if len(tools) != 0 {
		t.Errorf("Expected 0 tools initially, got %d", len(tools))
	}

	// Add a tool
	toolDir := filepath.Join(tempDir, "test_tool")
	os.MkdirAll(toolDir, 0755)
	toolTOML := []byte(`
name = "test"
description = "Test tool"
script = "script.py"
`)
	os.WriteFile(filepath.Join(toolDir, "tool.toml"), toolTOML, 0644)
	os.WriteFile(filepath.Join(toolDir, "script.py"), []byte("import llmr.mcp\ndef main():\n    llmr.mcp.return_string('ok')\n"), 0644)

	// Tool should now be visible
	tools, _ = provider.GetTools(context.Background())
	if len(tools) != 1 {
		t.Errorf("Expected 1 tool after adding, got %d", len(tools))
	}
	if len(tools) > 0 && tools[0].Name != "test" {
		t.Errorf("Expected tool name 'test', got '%s'", tools[0].Name)
	}

	// Modify tool description
	modifiedTOML := []byte(`
name = "test"
description = "Modified description"
keywords = ["modified"]
script = "script.py"
`)
	os.WriteFile(filepath.Join(toolDir, "tool.toml"), modifiedTOML, 0644)

	// Changes should be picked up
	tools, _ = provider.GetTools(context.Background())
	if len(tools) != 1 {
		t.Errorf("Expected 1 tool after modification, got %d", len(tools))
	}
	if len(tools) > 0 && tools[0].Description != "Modified description" {
		t.Errorf("Expected modified description, got '%s'", tools[0].Description)
	}

	// Remove tool
	os.RemoveAll(toolDir)

	// Tool should be gone
	tools, _ = provider.GetTools(context.Background())
	if len(tools) != 0 {
		t.Errorf("Expected 0 tools after removal, got %d", len(tools))
	}
}

// TestToolParameters tests different parameter types
func TestToolParameters(t *testing.T) {
	tempDir := t.TempDir()

	toolDir := filepath.Join(tempDir, "param_tool")
	os.MkdirAll(toolDir, 0755)
	toolTOML := []byte(`
name = "param_test"
description = "Tool with various parameters"
script = "script.py"

[parameters.str_param]
type = "string"
description = "A string parameter"
required = true

[parameters.num_param]
type = "number"
description = "A number parameter"
required = false

[parameters.bool_param]
type = "boolean"
description = "A boolean parameter"
required = false
`)
	os.WriteFile(filepath.Join(toolDir, "tool.toml"), toolTOML, 0644)
	os.WriteFile(filepath.Join(toolDir, "script.py"), []byte("import llmr.mcp\ndef main():\n    llmr.mcp.return_string('ok')\n"), 0644)

	config := &Config{
		Scriptling: ScriptlingConfig{
			ToolsPath: tempDir,
		},
	}

	mcpServer := &MCPServer{
		config:    config,
		logger:    &testLogger{},
		toolsPath: tempDir,
	}

	provider := NewScriptToolProvider(mcpServer)
	tools, err := provider.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools failed: %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name != "param_test" {
		t.Errorf("Expected tool name 'param_test', got '%s'", tool.Name)
	}

	// Verify input schema exists
	if tool.InputSchema == nil {
		t.Error("Expected input schema to be present")
	}
}
