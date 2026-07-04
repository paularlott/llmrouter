package router

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	mcp_lib "github.com/paularlott/mcp"
	mcpcli "github.com/paularlott/scriptling/scriptling-cli/mcp"
)

func TestScanToolsFolder(t *testing.T) {
	// Create a temporary directory for test tools
	tmpDir, err := os.MkdirTemp("", "scriptling-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test tool files
	addToml := `
description = "Test add tool"
keywords = ["test", "add"]

[[parameters]]
name = "a"
type = "int"
description = "First number"
required = true

[[parameters]]
name = "b"
type = "int"
description = "Second number"
required = true
`
	if err := os.WriteFile(filepath.Join(tmpDir, "add.toml"), []byte(addToml), 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}

	// Create a corresponding .py file
	addPy := `
print("Hello from add tool")
`
	if err := os.WriteFile(filepath.Join(tmpDir, "add.py"), []byte(addPy), 0644); err != nil {
		t.Fatalf("failed to write test py: %v", err)
	}

	// Create a discoverable tool
	discoverableToml := `
description = "Test discoverable tool"
discoverable = true
keywords = ["test"]

[[parameters]]
name = "message"
type = "string"
description = "A message"
required = false
`
	if err := os.WriteFile(filepath.Join(tmpDir, "discoverable.toml"), []byte(discoverableToml), 0644); err != nil {
		t.Fatalf("failed to write discoverable toml: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "discoverable.py"), []byte("print('discoverable')"), 0644); err != nil {
		t.Fatalf("failed to write discoverable py: %v", err)
	}

	// Scan the folder
	tools, err := mcpcli.ScanToolsFolder(tmpDir)
	if err != nil {
		t.Fatalf("ScanToolsFolder failed: %v", err)
	}

	// Should have found 2 tools
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	// Check add tool
	addMeta, ok := tools["add"]
	if !ok {
		t.Fatal("add tool not found")
	}
	if addMeta.Description != "Test add tool" {
		t.Errorf("expected description 'Test add tool', got '%s'", addMeta.Description)
	}
	if len(addMeta.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(addMeta.Parameters))
	}
	if addMeta.Parameters[0].Name != "a" {
		t.Errorf("expected first parameter name 'a', got '%s'", addMeta.Parameters[0].Name)
	}
	if addMeta.Parameters[0].Type != "int" {
		t.Errorf("expected first parameter type 'int', got '%s'", addMeta.Parameters[0].Type)
	}
	if !addMeta.Parameters[0].Required {
		t.Error("first parameter should be required")
	}

	// Check discoverable tool
	discoverableMeta, ok := tools["discoverable"]
	if !ok {
		t.Fatal("discoverable tool not found")
	}
	if !discoverableMeta.Discoverable {
		t.Error("discoverable tool should have Discoverable=true")
	}
}

func TestScanToolsFolder_EmptyDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scriptling-tools-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tools, err := mcpcli.ScanToolsFolder(tmpDir)
	if err != nil {
		t.Fatalf("ScanToolsFolder failed: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools in empty directory, got %d", len(tools))
	}
}

func TestScanToolsFolder_IgnoresNonTOMLFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scriptling-tools-non-toml-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a .toml file without .py counterpart (should still be scanned)
	if err := os.WriteFile(filepath.Join(tmpDir, "tool1.toml"), []byte("description = \"test\""), 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}

	// Create a .py file without .toml counterpart (should be ignored)
	if err := os.WriteFile(filepath.Join(tmpDir, "tool2.py"), []byte("print('test')"), 0644); err != nil {
		t.Fatalf("failed to write test py: %v", err)
	}

	// Create a subdirectory (should be ignored)
	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	tools, err := mcpcli.ScanToolsFolder(tmpDir)
	if err != nil {
		t.Fatalf("ScanToolsFolder failed: %v", err)
	}

	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if _, ok := tools["tool1"]; !ok {
		t.Error("tool1 should be found")
	}
	if _, ok := tools["tool2"]; ok {
		t.Error("tool2 should not be found (no .toml)")
	}
}

func TestScriptlingToolManager_New(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scriptling-manager-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a simple tool
	if err := os.WriteFile(filepath.Join(tmpDir, "test.toml"), []byte(`
description = "Test tool"
[[parameters]]
name = "msg"
type = "string"
description = "Message"
required = false
`), 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "test.py"), []byte("print('test')"), 0644); err != nil {
		t.Fatalf("failed to write test py: %v", err)
	}

	config := types.ScriptingConfig{
		ToolsDir: tmpDir,
	}

	mainServer := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(config, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager failed: %v", err)
	}
	defer manager.Shutdown()

	// Verify tools are registered on the main server
	tools := mainServer.ListTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool to be registered on main server")
	}
}

func TestScriptlingToolManager_NoToolsDir(t *testing.T) {
	config := types.ScriptingConfig{}

	mainServer := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(config, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager failed: %v", err)
	}
	defer manager.Shutdown()

	// Should work without tools dir
}

func TestCreateMCPToolHandler(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scriptling-handler-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	script := `
import scriptling.mcp.tool as tool
result = "Hello, " + tool.get_string("name", "World")
tool.return_string(result)
`
	scriptPath := filepath.Join(tmpDir, "test.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0644); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}

	handler, err := mcpcli.BuildToolHandler(scriptPath, mcpcli.NewHandlerConfig(nil))
	if err != nil {
		t.Fatalf("BuildToolHandler failed: %v", err)
	}

	ctx := context.Background()

	resp, err := handler(ctx, mcp_lib.NewToolRequest(nil))
	if err != nil {
		t.Fatalf("handler failed with default params: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	resp2, err := handler(ctx, mcp_lib.NewToolRequest(map[string]interface{}{"name": "Test"}))
	if err != nil {
		t.Fatalf("handler failed with custom params: %v", err)
	}
	if resp2 == nil {
		t.Fatal("expected response, got nil")
	}
	_ = resp
}

func TestScriptlingToolManager_Shutdown(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scriptling-shutdown-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "test.toml"), []byte(`
description = "Test"
[[parameters]]
name = "x"
type = "string"
required = false
`), 0644); err != nil {
		t.Fatalf("failed to write test toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "test.py"), []byte("print('test')"), 0644); err != nil {
		t.Fatalf("failed to write test py: %v", err)
	}

	config := types.ScriptingConfig{
		ToolsDir: tmpDir,
	}

	mainServer := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(config, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager failed: %v", err)
	}

	// Shutdown should not panic or hang
	done := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}
