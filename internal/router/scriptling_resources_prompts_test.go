package router

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	mcplib "github.com/paularlott/mcp"
)

// writeFile writes data to path, creating intermediate directories.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

// pipeClientServer wires an mcplib stream client to the given server over
// in-process pipes and returns the client plus a cleanup function. Mirrors the
// helper in scriptling-cli/server/mcp_resources_test.go so the test exercises
// the full protocol path (initialize -> list/read).
func pipeClientServer(t *testing.T, mcpServer *mcplib.Server) (*mcplib.Client, func()) {
	t.Helper()
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = mcpServer.ServeStream(ctx, serverReader, serverWriter)
	}()

	client := mcplib.NewStreamClient(clientReader, clientWriter, "")
	cleanup := func() {
		cancel()
		client.Close()
		clientWriter.Close()
		serverReader.Close()
		<-serveDone
		serverWriter.Close()
		clientReader.Close()
	}
	return client, cleanup
}

// TestScriptlingManagerResourcesAndPrompts sets up a manager with all three
// kinds of scriptling content and verifies each is reachable through the MCP
// protocol: tools/list, resources/list + resources/templates/list + read on
// both static and template URIs, and prompts/list + prompts/get on both static
// and dynamic prompts.
func TestScriptlingManagerResourcesAndPrompts(t *testing.T) {
	toolsDir := t.TempDir()
	resDir := t.TempDir()
	promptsDir := t.TempDir()

	// One tool.
	writeFile(t, filepath.Join(toolsDir, "greet.toml"),
		[]byte("description = \"Greet\"\nkeywords=[\"hi\"]\n[[parameters]]\nname=\"name\"\ntype=\"string\"\ndescription=\"Name\"\nrequired=true\n"))
	writeFile(t, filepath.Join(toolsDir, "greet.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('hi ' + tool.get_string('name'))\n"))

	// Resources: static file under docs/ scheme + template greeting://{name}.
	writeFile(t, filepath.Join(resDir, "docs/readme.md"),
		[]byte("# Hello\nThis is a static resource."))
	writeFile(t, filepath.Join(resDir, "greeting/{name}.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_string('Hello, ' + tool.get_string('name') + '!')\n"))

	// Prompts: one static .md, one dynamic toml+py.
	writeFile(t, filepath.Join(promptsDir, "static.md"),
		[]byte("Summarise the following content."))
	writeFile(t, filepath.Join(promptsDir, "review.toml"),
		[]byte("description = \"Review code\"\n[[arguments]]\nname=\"language\"\ntype=\"string\"\ndescription=\"Language\"\nrequired=true\n"))
	writeFile(t, filepath.Join(promptsDir, "review.py"),
		[]byte("import scriptling.mcp.tool as tool\ntool.return_object({\"messages\": [{\"role\": \"user\", \"content\": \"Review this \" + tool.get_string('language') + \" code.\"}]})\n"))

	mainServer := mcplib.NewServer("llmrouter-test", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		ToolsDir:     toolsDir,
		ResourcesDir: resDir,
		PromptsDir:   promptsDir,
	}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()
	ctx := context.Background()

	// Tools show up via tools/list.
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if !containsToolName(tools, "greet") {
		t.Fatalf("expected greet tool, got %+v", toolNames(tools))
	}

	// Static resource shows up via resources/list.
	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if !containsResourceURI(resources, "docs://readme.md") {
		t.Fatalf("expected docs://readme.md, got %+v", resourceURIs(resources))
	}

	// Template shows up via resources/templates/list.
	templates, err := client.ListResourceTemplates(ctx)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if !containsTemplateURI(templates, "greeting://{name}") {
		t.Fatalf("expected greeting://{name} template, got %+v", templateURIs(templates))
	}

	// Read static resource.
	rr, err := client.ReadResource(ctx, "docs://readme.md")
	if err != nil {
		t.Fatalf("ReadResource static: %v", err)
	}
	if len(rr.Contents) != 1 || !strings.Contains(rr.Contents[0].Text, "static resource") {
		t.Fatalf("unexpected static resource body: %+v", rr)
	}

	// Read expanded template — runs the .py handler.
	rg, err := client.ReadResource(ctx, "greeting://Ada")
	if err != nil {
		t.Fatalf("ReadResource template: %v", err)
	}
	if len(rg.Contents) != 1 || rg.Contents[0].Text != "Hello, Ada!" {
		t.Fatalf("unexpected template body: %+v", rg)
	}

	// Prompts list shows both static and dynamic.
	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if !containsPromptName(prompts, "static") || !containsPromptName(prompts, "review") {
		t.Fatalf("expected static + review prompts, got %+v", promptNamesList(prompts))
	}

	// Get static prompt.
	ps, err := client.GetPrompt(ctx, "static", nil)
	if err != nil {
		t.Fatalf("GetPrompt static: %v", err)
	}
	if len(ps.Messages) != 1 || !strings.Contains(ps.Messages[0].Content.Text, "Summarise") {
		t.Fatalf("unexpected static prompt body: %+v", ps)
	}

	// Get dynamic prompt with arg.
	pr, err := client.GetPrompt(ctx, "review", map[string]string{"language": "go"})
	if err != nil {
		t.Fatalf("GetPrompt dynamic: %v", err)
	}
	if len(pr.Messages) != 1 || !strings.Contains(pr.Messages[0].Content.Text, "Review this go code") {
		t.Fatalf("unexpected dynamic prompt body: %+v", pr)
	}
}

// TestScriptlingManagerResourcesOnlyDir verifies that a manager configured with
// only a resources dir registers resources and not tools/prompts.
func TestScriptlingManagerResourcesOnlyDir(t *testing.T) {
	resDir := t.TempDir()
	writeFile(t, filepath.Join(resDir, "data/info.txt"), []byte("info"))

	mainServer := mcplib.NewServer("t", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		ResourcesDir: resDir,
	}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()

	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if !containsResourceURI(resources, "data://info.txt") {
		t.Fatalf("expected data://info.txt, got %+v", resourceURIs(resources))
	}
}

// TestScriptlingManagerPromptsOnlyDir verifies that a manager configured with
// only a prompts dir registers prompts.
func TestScriptlingManagerPromptsOnlyDir(t *testing.T) {
	promptsDir := t.TempDir()
	writeFile(t, filepath.Join(promptsDir, "tips.md"), []byte("Be concise."))

	mainServer := mcplib.NewServer("t", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		PromptsDir: promptsDir,
	}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()

	prompts, err := client.ListPrompts(context.Background())
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if !containsPromptName(prompts, "tips") {
		t.Fatalf("expected tips prompt, got %+v", promptNamesList(prompts))
	}
}

// waitFor repeatedly calls fn until it returns true or the timeout elapses.
// Used to absorb the multiple intermediate notifications mcplib emits during
// an in-place reload (every Register/Unregister fires its own listChanged).
func waitFor(t *testing.T, what string, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// TestScriptlingManagerPromptsReload verifies that rewriting a prompt file
// triggers a debounced in-place reload that connected clients are notified
// about and that picks up the new content.
func TestScriptlingManagerPromptsReload(t *testing.T) {
	promptsDir := t.TempDir()
	promptPath := filepath.Join(promptsDir, "growing.md")
	writeFile(t, promptPath, []byte("v1"))

	mainServer := mcplib.NewServer("t", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		PromptsDir: promptsDir,
	}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()
	ctx := context.Background()

	// Prime the client cache.
	if _, err := client.ListPrompts(ctx); err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	notify := make(chan struct{}, 16)
	client.OnPromptsChanged(func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})

	// Edit the prompt file.
	writeFile(t, promptPath, []byte("v2-reloaded"))

	// Wait for at least one notification, then poll until the new content is
	// visible. mcplib emits an intermediate notification after Unregister
	// (before Register) so the first notify may arrive when the prompt is
	// briefly absent — the polling absorbs that.
	select {
	case <-notify:
	case <-time.After(3 * time.Second):
		t.Fatal("OnPromptsChanged did not fire after prompt edit")
	}

	ok := waitFor(t, "prompt body to refresh", 3*time.Second, func() bool {
		pr, err := client.GetPrompt(ctx, "growing", nil)
		if err != nil || len(pr.Messages) != 1 {
			return false
		}
		return pr.Messages[0].Content.Text == "v2-reloaded"
	})
	if !ok {
		t.Fatal("prompt body never reflected v2-reloaded after reload")
	}
}

// TestScriptlingManagerResourcesReload verifies that adding a new static
// resource file triggers a reload that exposes it.
func TestScriptlingManagerResourcesReload(t *testing.T) {
	resDir := t.TempDir()

	mainServer := mcplib.NewServer("t", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{
		ResourcesDir: resDir,
	}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	defer manager.Shutdown()

	client, cleanup := pipeClientServer(t, mainServer)
	defer cleanup()
	ctx := context.Background()

	if _, err := client.ListResources(ctx); err != nil {
		t.Fatalf("ListResources (initial): %v", err)
	}

	notify := make(chan struct{}, 16)
	client.OnResourcesChanged(func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	})

	// Drop a new file into the watched top level.
	writeFile(t, filepath.Join(resDir, "data/new.txt"), []byte("fresh"))

	select {
	case <-notify:
	case <-time.After(3 * time.Second):
		t.Fatal("OnResourcesChanged did not fire after adding resource")
	}

	// mcplib emits intermediate notifications during in-place reload (every
	// Unregister/Register fires its own listChanged) — poll until the new
	// resource is visible.
	ok := waitFor(t, "data://new.txt to appear", 3*time.Second, func() bool {
		resources, err := client.ListResources(ctx)
		if err != nil {
			return false
		}
		return containsResourceURI(resources, "data://new.txt")
	})
	if !ok {
		resources, _ := client.ListResources(ctx)
		t.Fatalf("data://new.txt never appeared after reload; got %+v", resourceURIs(resources))
	}
}

// TestScriptlingManagerNoDirs verifies the manager constructs and shuts down
// cleanly with no source folders configured.
func TestScriptlingManagerNoDirs(t *testing.T) {
	mainServer := mcplib.NewServer("t", "1.0")
	manager, err := NewScriptlingToolManager(types.ScriptingConfig{}, mainServer, &testLogger{})
	if err != nil {
		t.Fatalf("NewScriptlingToolManager: %v", err)
	}
	manager.Shutdown()
}

// Helper conversions on mcplib result types to keep assertions terse.

func toolNames(tools []mcplib.MCPTool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}
func containsToolName(tools []mcplib.MCPTool, want string) bool {
	for _, t := range tools {
		if t.Name == want {
			return true
		}
	}
	return false
}
func resourceURIs(rs []mcplib.MCPResource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.URI
	}
	return out
}
func containsResourceURI(rs []mcplib.MCPResource, want string) bool {
	for _, r := range rs {
		if r.URI == want {
			return true
		}
	}
	return false
}
func templateURIs(ts []mcplib.MCPResourceTemplate) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.URITemplate
	}
	return out
}
func containsTemplateURI(ts []mcplib.MCPResourceTemplate, want string) bool {
	for _, t := range ts {
		if t.URITemplate == want {
			return true
		}
	}
	return false
}
func promptNamesList(ps []mcplib.MCPPrompt) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}
func containsPromptName(ps []mcplib.MCPPrompt, want string) bool {
	for _, p := range ps {
		if p.Name == want {
			return true
		}
	}
	return false
}
