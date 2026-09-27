package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	manager, err := NewScriptlingToolManager(config, &testLogger{}, mainServer)
	if err != nil {
		t.Fatalf("NewScriptlingToolManager failed: %v", err)
	}
	defer manager.Shutdown()

	// Verify tools are registered on the main server
	tools := mainServer.ListToolsWithContext(context.Background())
	if len(tools) == 0 {
		t.Fatal("expected at least one tool to be registered on main server")
	}
}

func TestScriptlingToolManager_NoToolsDir(t *testing.T) {
	config := types.ScriptingConfig{}

	mainServer := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(config, &testLogger{}, mainServer)
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
	manager, err := NewScriptlingToolManager(config, &testLogger{}, mainServer)
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

// A skills directory registers one skill per .md file on every server the
// manager serves, and the extension capability is declared.
// A skills directory registers one skill per subdirectory with a SKILL.md
// (Agent Skills format), files becoming readable resources, on every server
// the manager serves.
func TestScriptlingSkillsServed(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "code-review")
	os.MkdirAll(filepath.Join(skillDir, "references"), 0o755)
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: code-review\ndescription: Review changesets\n---\n\nRead the diff twice."))
	writeFile(t, filepath.Join(skillDir, "references", "checklist.md"), []byte("Checklist."))

	mainServer := mcp_lib.NewServer("test", "1.0")
	endpointServer := mcp_lib.NewServer("test-endpoint", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		SkillsDir: dir,
	}, &testLogger{}, mainServer, endpointServer)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer manager.Shutdown()

	for _, s := range []*mcp_lib.Server{mainServer, endpointServer} {
		skills := s.ListSkills()
		if len(skills) != 1 || skills[0].URI != "skill://code-review/SKILL.md" {
			t.Fatalf("skills = %+v", skills)
		}
		if skills[0].Frontmatter["description"] != "Review changesets" {
			t.Fatalf("frontmatter = %+v", skills[0].Frontmatter)
		}
		if len(skills[0].Resources) != 2 {
			t.Fatalf("resources = %+v", skills[0].Resources)
		}
		res, err := s.ReadResource(context.Background(), "skill://code-review/references/checklist.md")
		if err != nil || !strings.Contains(res.Contents[0].Text, "Checklist.") {
			t.Fatalf("skill file read = (%+v, %v)", res, err)
		}
	}
}

// Skills changes are picked up without a restart: the skills tree is
// watched recursively, so adding a skill directory, editing files inside an
// existing skill, and removing a skill all reach the live servers before
// the next client skills/list.
func TestScriptlingSkillsReloadOnDiskChange(t *testing.T) {
	dir := t.TempDir()
	firstDir := filepath.Join(dir, "first-skill")
	os.MkdirAll(firstDir, 0o755)
	writeFile(t, filepath.Join(firstDir, "SKILL.md"), []byte("---\nname: first-skill\ndescription: First\n---\n\nOriginal body."))

	mainServer := mcp_lib.NewServer("test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		SkillsDir: dir,
	}, &testLogger{}, mainServer)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer manager.Shutdown()

	skillURIs := func() []string {
		var uris []string
		for _, sk := range mainServer.ListSkills() {
			uris = append(uris, sk.URI)
		}
		return uris
	}
	waitForSkills := func(t *testing.T, want int, contains, missing string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			uris := skillURIs()
			ok := len(uris) == want
			for _, u := range uris {
				if missing != "" && u == missing {
					ok = false
				}
			}
			if contains != "" {
				found := false
				for _, u := range uris {
					if u == contains {
						found = true
					}
				}
				ok = ok && found
			}
			if ok {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("skills never reached want=%d contains=%q missing=%q, got %v", want, contains, missing, skillURIs())
	}

	waitForSkills(t, 1, "skill://first-skill/SKILL.md", "")

	// Add a second skill (a whole new directory of files) and edit the
	// first skill's SKILL.md in place — both must reload.
	secondDir := filepath.Join(dir, "second-skill")
	os.MkdirAll(secondDir, 0o755)
	writeFile(t, filepath.Join(secondDir, "SKILL.md"), []byte("---\nname: second-skill\ndescription: Second\n---\n\nSecond body."))
	writeFile(t, filepath.Join(firstDir, "SKILL.md"), []byte("---\nname: first-skill\ndescription: First\n---\n\nEdited body."))
	waitForSkills(t, 2, "skill://second-skill/SKILL.md", "")

	// The edited SKILL.md content is what clients read now.
	res, err := mainServer.ReadResource(context.Background(), "skill://first-skill/SKILL.md")
	if err != nil || !strings.Contains(res.Contents[0].Text, "Edited body.") {
		t.Fatalf("edited SKILL.md not served: (%+v, %v)", res, err)
	}

	// Removing a skill directory drops it from the listing on reload.
	if err := os.RemoveAll(secondDir); err != nil {
		t.Fatal(err)
	}
	waitForSkills(t, 1, "skill://first-skill/SKILL.md", "skill://second-skill/SKILL.md")
}

// An unpacked MCP app package (manifest.toml serve=["mcp"]) is served in
// full: tools, resources (including ui:// app views with the MCP Apps MIME
// type), prompts and skills — with the UI Apps extension advertised.
func TestScriptlingServesMCAppPackage(t *testing.T) {
	appDir := t.TempDir()
	writeFile(t, filepath.Join(appDir, "manifest.toml"),
		[]byte("name = \"demo-app\"\nversion = \"1.2.3\"\nserve = [\"mcp\"]\n"))

	writeFile(t, filepath.Join(appDir, "tools", "greet.toml"),
		[]byte("description = \"Greet\"\nkeywords=[\"hi\"]\n[[parameters]]\nname=\"name\"\ntype=\"string\"\ndescription=\"Name\"\nrequired=true\n"))
	writeFile(t, filepath.Join(appDir, "tools", "greet.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('hi ' + tool.get_string('name'))\n"))

	if err := os.MkdirAll(filepath.Join(appDir, "resources", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(appDir, "resources", "ui", "dashboard.html"),
		[]byte("<html><body>Sales dashboard</body></html>"))

	writeFile(t, filepath.Join(appDir, "prompts", "hint.md"),
		[]byte("Summarize the sales dashboard."))

	skillDir := filepath.Join(appDir, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: demo-skill\ndescription: Demo app skill\n---\nUse the dashboard."))

	mainServer := mcp_lib.NewServer("llmrouter-app-test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		AppDir: appDir,
	}, &testLogger{}, mainServer)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()
	ctx := context.Background()

	// Tools from the package.
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if !containsToolName(tools, "greet") {
		t.Fatalf("expected greet tool, got %+v", toolNames(tools))
	}

	// The ui:// app view is a resource with the MCP Apps MIME type.
	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	foundUI := false
	for _, r := range resources {
		if r.URI == "ui://dashboard.html" {
			foundUI = true
			if r.MimeType != mcp_lib.UIAppMimeType {
				t.Fatalf("ui resource mime = %q, want %q", r.MimeType, mcp_lib.UIAppMimeType)
			}
		}
	}
	if !foundUI {
		t.Fatalf("ui://dashboard.html not listed: %+v", resources)
	}
	uiRes, err := client.ReadResource(ctx, "ui://dashboard.html")
	if err != nil || len(uiRes.Contents) == 0 || !strings.Contains(uiRes.Contents[0].Text, "Sales dashboard") {
		t.Fatalf("ui read = (%+v, %v)", uiRes, err)
	}
	if uiRes.Contents[0].MimeType != mcp_lib.UIAppMimeType {
		t.Fatalf("ui read mime = %q, want %q", uiRes.Contents[0].MimeType, mcp_lib.UIAppMimeType)
	}

	// Prompts and skills from the package.
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	foundPrompt := false
	for _, p := range prompts {
		if p.Name == "hint" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("hint prompt not listed: %+v", prompts)
	}
	skills := mainServer.ListSkills()
	if len(skills) != 1 || skills[0].URI != "skill://demo-skill/SKILL.md" {
		t.Fatalf("skills = %+v", skills)
	}
}

// A package whose manifest does not serve "mcp" is rejected at startup.
func TestScriptlingRejectsNonMCPAppPackage(t *testing.T) {
	appDir := t.TempDir()
	writeFile(t, filepath.Join(appDir, "manifest.toml"),
		[]byte("name = \"web-only\"\nversion = \"0.1.0\"\nserve = [\"http\"]\n"))
	writeFile(t, filepath.Join(appDir, "webroot", "index.html"), []byte("<html></html>"))

	mainServer := mcp_lib.NewServer("llmrouter-app-test", "1.0")
	_, err := NewScriptlingToolManager(types.ScriptingConfig{
		AppDir: appDir,
	}, &testLogger{}, mainServer)
	if err == nil {
		t.Fatal("expected error for app package that does not serve mcp")
	}
	if !strings.Contains(err.Error(), "does not serve") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A packed .zip is scriptling's to serve: llmrouter's app_dir is
// disk-served directories only (the watcher needs real directories).
func TestScriptlingRejectsZipAppPackage(t *testing.T) {
	mainServer := mcp_lib.NewServer("llmrouter-app-test", "1.0")
	_, err := NewScriptlingToolManager(types.ScriptingConfig{
		AppDir: "/tmp/some-app.zip",
	}, &testLogger{}, mainServer)
	if err == nil {
		t.Fatal("expected error for zip app_dir")
	}
	if !strings.Contains(err.Error(), "scriptling --package") {
		t.Fatalf("error should point at scriptling for packed apps: %v", err)
	}
}
