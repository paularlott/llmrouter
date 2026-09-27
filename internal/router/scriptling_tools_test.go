package router

import (
	"io"
	"encoding/json"
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




// The admin UI's "local" namespace reaches llmrouter's own scriptling-served
// content: the list contains the local entry, tools list and call execute
// in-process, and mutating endpoints reject the reserved namespace.
func TestLocalServerAdminEntry(t *testing.T) {
	appDir := t.TempDir()
	writeFile(t, filepath.Join(appDir, "tools", "echo.toml"),
		[]byte("description = \"Echo\"\nkeywords=[\"test\"]\n[[parameters]]\nname=\"text\"\ntype=\"string\"\ndescription=\"Text\"\nrequired=true\n"))
	writeFile(t, filepath.Join(appDir, "tools", "echo.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('echo: ' + tool.get_string('text'))\n"))
	writeFile(t, filepath.Join(appDir, "prompts", "note.md"), []byte("Say something nice."))

	r, err := NewRouter(&types.Config{
		Server: types.ServerConfig{Host: "127.0.0.1", Port: 0},
		Scripting: types.ScriptingConfig{
			ToolsDir:   filepath.Join(appDir, "tools"),
			PromptsDir: filepath.Join(appDir, "prompts"),
		},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// App-linked tools carry the app flag in the local listing too.
	writeFile(t, filepath.Join(appDir, "tools", "sales_summary.toml"),
		[]byte("description = \"Summarize sales\"\n[ui]\nresourceUri = \"ui://sales.html\"\n"))
	writeFile(t, filepath.Join(appDir, "tools", "sales_summary.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('region=all')\n"))

	// The local entry is present and flagged.
	info, ok := r.localServerInfo()
	if !ok {
		t.Fatal("local server info missing with scripting configured")
	}
	if info.Namespace != "local" || !info.LocalServer || !info.Enabled {
		t.Fatalf("local info = %+v", info)
	}
	servers := r.getMCPServers()
	found := false
	for _, s := range servers {
		if s.Namespace == "local" && s.LocalServer {
			found = true
		}
	}
	if !found {
		t.Fatalf("local entry not in server list: %+v", servers)
	}

	// Tools list and execute through the local namespace.
	tools, err := r.getMCPTools("local")
	if err != nil {
		t.Fatalf("getMCPTools(local): %v", err)
	}
	hasEcho := false
	for _, tl := range tools {
		if tl.Name == "echo" {
			hasEcho = true
			if !tl.Enabled || tl.InputSchema == nil {
				t.Fatalf("echo tool info = %+v", tl)
			}
		}
	}
	if !hasEcho {
		t.Fatalf("echo tool not listed: %+v", tools)
	}
	r.mcpServer.scriptlingManager.handleToolCreate("sales_summary")
	localTools, err := r.getMCPTools("local")
	if err != nil {
		t.Fatalf("getMCPTools(local) after add: %v", err)
	}
	sawSalesApp := false
	for _, tl := range localTools {
		if tl.Name == "sales_summary" && tl.IsApp {
			sawSalesApp = true
		}
	}
	if !sawSalesApp {
		t.Fatalf("sales_summary not marked as app in local listing: %+v", localTools)
	}

	result, err := r.callMCPTool("local", "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("callMCPTool(local): %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "echo: hi" {
		t.Fatalf("local echo result = %+v", result)
	}

	// Prompts and resources also list.
	prompts, err := r.getMCPPrompts("local")
	if err != nil || len(prompts) != 1 || prompts[0].Name != "note" {
		t.Fatalf("local prompts = (%+v, %v)", prompts, err)
	}
	resources, err := r.getMCPResources("local")
	if err != nil {
		t.Fatalf("local resources: %v", err)
	}
	_ = resources // none in this fixture; listing must simply not error

	// Without scripting configured there is no local entry.
	r2, err := NewRouter(&types.Config{
		Server:    types.ServerConfig{Host: "127.0.0.1", Port: 0},
		Providers: []types.ProviderConfig{},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter(bare): %v", err)
	}
	if _, ok := r2.localServerInfo(); ok {
		t.Fatal("local entry must be absent without scripting content")
	}
	for _, s := range r2.getMCPServers() {
		if s.Namespace == "local" {
			t.Fatal("local entry leaked into list without scripting")
		}
	}
}

// The local entry is pinned to the top of the admin server list, ahead of
// the alphabetically-sorted remotes.
func TestLocalServerPinnedToTop(t *testing.T) {
	appDir := t.TempDir()
	writeFile(t, filepath.Join(appDir, "note.md"), []byte("Note."))

	r, err := NewRouter(&types.Config{
		Server:    types.ServerConfig{Host: "127.0.0.1", Port: 0},
		MCP:       types.MCPConfig{RemoteServers: []types.MCPRemoteServerConfig{{Namespace: "alpha", URL: "http://alpha"}, {Namespace: "zulu", URL: "http://zulu"}}},
		Scripting: types.ScriptingConfig{PromptsDir: appDir},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	servers := r.getMCPServers()
	if len(servers) != 3 {
		t.Fatalf("want 3 servers, got %d: %+v", len(servers), servers)
	}
	if servers[0].Namespace != "local" || !servers[0].LocalServer {
		t.Fatalf("local must be first, got %+v", servers)
	}
	if servers[1].Namespace != "alpha" || servers[2].Namespace != "zulu" {
		t.Fatalf("remotes must stay alphabetical after local: %+v", servers)
	}
}

// Prompts render through the admin test path: the local namespace renders
// in-process via prompts/get semantics.
func TestLocalPromptRender(t *testing.T) {
	promptsDir := t.TempDir()
	writeFile(t, filepath.Join(promptsDir, "greet.md"),
		[]byte("Say hello to nobody in particular."))

	r, err := NewRouter(&types.Config{
		Server:    types.ServerConfig{Host: "127.0.0.1", Port: 0},
		Scripting: types.ScriptingConfig{PromptsDir: promptsDir},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	result, err := r.callMCPPrompt("local", "greet", map[string]string{})
	if err != nil {
		t.Fatalf("callMCPPrompt(local): %v", err)
	}
	if len(result.Messages) == 0 || result.Messages[0].Text != "Say hello to nobody in particular." {
		t.Fatalf("rendered prompt = %+v", result)
	}
}

// App-shaped content works through the plain directory flags: a ui://
// resource in the resources tree carries the MCP Apps MIME type and its
// presence declares the UI Apps extension — no manifest, no app flag.
func TestUIResourcesViaDirFlagsDeclareAppsExtension(t *testing.T) {
	toolsDir := t.TempDir()
	resDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(resDir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(resDir, "ui", "sales.html"),
		[]byte("<html><body>Sales dashboard</body></html>"))
	writeFile(t, filepath.Join(toolsDir, "greet.toml"),
		[]byte("description = \"Greet\"\nkeywords=[\"hi\"]\n[[parameters]]\nname=\"name\"\ntype=\"string\"\ndescription=\"Name\"\nrequired=true\n"))
	writeFile(t, filepath.Join(toolsDir, "greet.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('hi ' + tool.get_string('name'))\n"))

	mainServer := mcp_lib.NewServer("llmrouter-test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		ToolsDir:     toolsDir,
		ResourcesDir: resDir,
	}, &testLogger{}, mainServer)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()
	ctx := context.Background()

	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	found := false
	for _, r := range resources {
		if r.URI == "ui://sales.html" {
			found = true
			if r.MimeType != mcp_lib.UIAppMimeType {
				t.Fatalf("ui mime = %q, want %q", r.MimeType, mcp_lib.UIAppMimeType)
			}
		}
	}
	if !found {
		t.Fatalf("ui://sales.html not listed: %+v", resources)
	}

	// A tool with a [ui] TOML table links its view via _meta.ui.resourceUri,
	// making it an MCP Apps tool (ToolIsApp).
	writeFile(t, filepath.Join(toolsDir, "sales_summary.toml"),
		[]byte("description = \"Summarize sales\"\n[ui]\nresourceUri = \"ui://sales.html\"\n\n[[icons]]\nsrc = \"data:image/svg+xml;base64,AAA\"\nmimeType = \"image/svg+xml\"\n"))
	writeFile(t, filepath.Join(toolsDir, "sales_summary.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('region=all')\n"))
	manager.handleToolCreate("sales_summary")
	deadline := time.Now().Add(5 * time.Second)
	var appTool *mcp_lib.MCPTool
	for time.Now().Before(deadline) {
		toolsNow, err := client.ListTools(ctx)
		if err == nil {
			for _, tl := range toolsNow {
				if tl.Name == "sales_summary" {
					appTool = &tl
				}
			}
		}
		if appTool != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if appTool == nil {
		t.Fatal("sales_summary tool never appeared")
	}
	if !mcp_lib.ToolIsApp(*appTool) {
		t.Fatalf("sales_summary is not an app tool: _meta=%+v", appTool.Meta)
	}
	ui, ok := appTool.Meta["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != "ui://sales.html" {
		t.Fatalf("ui link = %+v", appTool.Meta)
	}
	if len(appTool.Icons) != 1 || !strings.HasPrefix(appTool.Icons[0].Src, "data:image/svg+xml;base64,") {
		t.Fatalf("app icon not carried on tools/list: %+v", appTool.Icons)
	}

	// The extension reaches clients in initialize: assert it via the raw
	// JSON-RPC handshake on a fresh pipe (the lib exposes no accessor).
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	defer func() { sw.Close(); cw.Close(); sr.Close(); cr.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mainServer.ServeStream(ctx, sr, sw) }()
	_, _ = cw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n"))
	buf := make([]byte, 8192)
	n, _ := cr.Read(buf)
	var resp struct {
		Result struct {
			Capabilities struct {
				Extensions map[string]any `json:"extensions"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("initialize response: %v (%s)", err, buf[:n])
	}
	if _, ok := resp.Result.Capabilities.Extensions[mcp_lib.UIAppsExtensionID]; !ok {
		t.Fatalf("UI Apps extension not advertised: %s", buf[:n])
	}
}

// The example dashboard view is real content: it stays under the resources
// tree with the app MIME type through the plain dir flags.
func TestExampleSalesDashboardServed(t *testing.T) {
	resDir := "../../examples/resources"
	if _, err := os.Stat(filepath.Join(resDir, "ui", "sales.html")); err != nil {
		t.Skipf("example resources not present: %v", err)
	}
	mainServer := mcp_lib.NewServer("llmrouter-example-test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		ResourcesDir: resDir,
	}, &testLogger{}, mainServer)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer manager.Shutdown()

	res, err := mainServer.ReadResource(context.Background(), "ui://sales.html")
	if err != nil || len(res.Contents) == 0 {
		t.Fatalf("read ui://sales.html = (%+v, %v)", res, err)
	}
	if res.Contents[0].MimeType != mcp_lib.UIAppMimeType {
		t.Fatalf("mime = %q, want the app MIME type", res.Contents[0].MimeType)
	}
	if !strings.Contains(res.Contents[0].Text, "<canvas") {
		t.Fatal("example view lost its canvas animation")
	}
	if !strings.Contains(res.Contents[0].Text, "requestAnimationFrame") {
		t.Fatal("example view lost its animation loop")
	}
	// The MCP Apps container contract: without size-changed reporting the
	// host's default iframe height wins and the view collapses to a strip.
	if !strings.Contains(res.Contents[0].Text, "ui/notifications/size-changed") {
		t.Fatal("example view lost its size reporting")
	}
	if !strings.Contains(res.Contents[0].Text, "ui/initialize") {
		t.Fatal("example view lost its host handshake")
	}
}
