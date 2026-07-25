package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	scriptlinglog "github.com/paularlott/logger"
	"github.com/paularlott/llmrouter/internal/types"
	mcp_lib "github.com/paularlott/mcp"
	"github.com/paularlott/mcp/toolmetadata"
	"github.com/paularlott/scriptling"
	scriptlingmcp "github.com/paularlott/scriptling/extlibs/mcp"
	"github.com/paularlott/scriptling/extlibs/secretprovider"
	scriptlingplugin "github.com/paularlott/scriptling/plugin"
	mcpcli "github.com/paularlott/scriptling/scriptling-cli/mcp"
	"github.com/paularlott/scriptling/scriptling-cli/setup"
	"github.com/paularlott/lmchatkit"
)

// scriptlingToolManager owns scriptling-served MCP content — tools, resources
// and prompts — registered on a single *mcp_lib.Server. It scans each
// configured source folder at startup, registers everything it finds, watches
// the folders for changes, and reloads in place (mutating the live server so
// connected clients keep their notification subscriptions). Reload granularity
// differs per kind: tools are re-registered per name (the only kind with
// declarative TOML metadata that pairs 1:1 with a .py), while resources and
// prompts are reloaded as a group because their scan logic interacts across
// sibling files (e.g. a name.toml shadows a name.md, template URIs depend on
// neighbouring path segments).
type scriptlingToolManager struct {
	logger           Logger
	config           types.ScriptingConfig
	toolsDirAbs      string
	resourcesDirAbs  string
	promptsDirAbs    string
	watcher          *fsnotify.Watcher
	plugins          *scriptlingplugin.Manager
	debounceDuration time.Duration
	debounceMu       sync.Mutex
	toolTimers       map[string]*time.Timer // per-name debounce for tool reloads
	resourceTimer    *time.Timer            // single debounce timer for full resources reload
	promptTimer      *time.Timer            // single debounce timer for full prompts reload
	done             chan struct{}
	wg               sync.WaitGroup
	mainServer       *mcp_lib.Server
	handlerCfg       mcpcli.HandlerConfig

	// Tracked keys so reload-in-place can unregister what it previously added.
	resourceStaticURIs []string // folder-sourced static resource URIs
	resourceTemplates  []string // folder-sourced resource template URIs
	promptNames        []string // folder-sourced prompt names

	// Optional SSE event broadcaster. When set, reload methods push
	// "resources_changed" / "prompts_changed" events
	// so all connected browser tabs refresh their cached lists without
	// polling.
	eventBroadcaster *lmchatkit.EventBroadcaster
}

// SetEventBroadcaster wires the SSE push notifier. When set, tool/resource/
// prompt reloads broadcast change events so all connected browser tabs
// refresh their cached lists without polling.
func (stm *scriptlingToolManager) SetEventBroadcaster(b *lmchatkit.EventBroadcaster) {
	stm.eventBroadcaster = b
}

func (stm *scriptlingToolManager) broadcast(eventType string) {
	if stm.eventBroadcaster != nil {
		stm.eventBroadcaster.Broadcast(lmchatkit.ServerEvent{Type: eventType})
	}
}

// NewScriptlingToolManager builds a manager, registers every folder-sourced
// tool / resource / prompt on mainServer, and starts watching for changes.
func NewScriptlingToolManager(config types.ScriptingConfig, mainServer *mcp_lib.Server, logger Logger) (*scriptlingToolManager, error) {
	stm := &scriptlingToolManager{
		config:           config,
		logger:           logger,
		mainServer:       mainServer,
		debounceDuration: 500 * time.Millisecond,
		done:             make(chan struct{}),
		toolTimers:       make(map[string]*time.Timer),
	}

	// Resolve absolute paths for each configured source folder.
	for _, pair := range []struct {
		src string
		dst *string
	}{
		{config.ToolsDir, &stm.toolsDirAbs},
		{config.ResourcesDir, &stm.resourcesDirAbs},
		{config.PromptsDir, &stm.promptsDirAbs},
	} {
		if pair.src != "" {
			abs, err := filepath.Abs(pair.src)
			if err != nil {
				abs = pair.src
			}
			*pair.dst = abs
		}
	}

	// Plugins are shared across every handler built from this manager.
	stm.plugins = scriptlingplugin.NewManager(logger, func(name string, err error) {
		logger.Error("Plugin process exited", "plugin", name, "error", err)
	})
	if len(config.PluginDirs) > 0 {
		for _, dir := range config.PluginDirs {
			stm.plugins.AddDir(dir)
		}
		if err := stm.plugins.Load(context.Background()); err != nil {
			return nil, fmt.Errorf("failed to load plugins: %w", err)
		}
		for _, warning := range stm.plugins.Warnings() {
			logger.Warn("Plugin load warning", "warning", warning)
		}
	}

	// The shared HandlerConfig drives every folder-sourced tool / resource /
	// prompt handler. LibPaths provides additional import dirs; the script's
	// own dir is prepended by each factory. No logger is wired through to the
	// interpreter (matching the pre-refactor behaviour); script execution
	// uses a null logger for the logging library.
	stm.handlerCfg = mcpcli.NewHandlerConfig(config.LibPaths,
		mcpcli.WithPlugins(stm.plugins),
	)

	if err := stm.registerAll(); err != nil {
		return nil, err
	}

	if config.ExecScript {
		stm.registerExecTool(stm.mainServer)
	}

	if stm.toolsDirAbs != "" || stm.resourcesDirAbs != "" || stm.promptsDirAbs != "" {
		if err := stm.startWatching(); err != nil {
			logger.Warn("Failed to start source-folder watcher, auto-reload disabled", "error", err)
		}
	}

	return stm, nil
}

// registerAll performs the initial scan+register pass for every configured
// folder. A scan failure on any one folder aborts startup so misconfiguration
// is surfaced loudly.
func (stm *scriptlingToolManager) registerAll() error {
	if stm.toolsDirAbs != "" {
		tools, err := mcpcli.ScanToolsFolder(stm.toolsDirAbs)
		if err != nil {
			return fmt.Errorf("failed to scan tools folder %s: %w", stm.toolsDirAbs, err)
		}
		for toolName, meta := range tools {
			stm.registerTool(toolName, meta)
		}
	}

	if stm.resourcesDirAbs != "" {
		staticURIs, templates, err := stm.registerResources()
		if err != nil {
			return fmt.Errorf("failed to register resources from %s: %w", stm.resourcesDirAbs, err)
		}
		stm.resourceStaticURIs = staticURIs
		stm.resourceTemplates = templates
	}

	if stm.promptsDirAbs != "" {
		names, err := stm.registerPrompts()
		if err != nil {
			return fmt.Errorf("failed to register prompts from %s: %w", stm.promptsDirAbs, err)
		}
		stm.promptNames = names
	}

	return nil
}

// newScriptling builds a fresh Scriptling interpreter configured the same way
// as the folder-sourced tool handlers (same library set, lib paths and plugins).
// It backs the built-in execute_script tool, which runs arbitrary caller code
// outside any fixed script directory.
func (stm *scriptlingToolManager) newScriptling() *scriptling.Scriptling {
	p := scriptling.New()
	setup.Scriptling(p, stm.config.LibPaths, false, nil, nil, secretprovider.NewRegistry(), scriptlinglog.NewNullLogger(), "", "")
	if stm.plugins != nil {
		scriptlingplugin.RegisterLibraries(p, stm.plugins)
	}
	return p
}

// registerExecTool registers the built-in execute_script MCP tool, mirroring
// scriptling's --mcp-exec-script. It runs arbitrary Scriptling code supplied by
// the caller and returns captured output (print() or an explicit
// return_string / return_object) — exactly like scriptling-cli's tool.
func (stm *scriptlingToolManager) registerExecTool(server *mcp_lib.Server) {
	server.RegisterTool(
		mcp_lib.NewTool("execute_script",
			`Execute Scriptling code and return the result. Scriptling is a Python 3-like scripting language.

KEY SYNTAX RULES:
- Use True/False (capitalized), None for null
- Use elif (not else if)
- 4-space indentation for blocks
- No nested classes, no multiple inheritance, no generators/yield

HTTP & JSON:
- HTTP response is an object: response.status_code, response.body, response.headers
- Use json.loads(str) and json.dumps(obj) for JSON
- Use msgpack.packb(obj) and msgpack.unpackb(bytes) for MessagePack binary serialization
- Use requests.get(url, options), requests.post(url, body, options) for HTTP
- Default HTTP timeout is 5 seconds
- HTTP options dict: {"timeout": 10, "headers": {"Authorization": "Bearer token"}}

COMMON PATTERNS:
- Dict iteration: for item in items(dict): key=item[0], value=item[1]
- List append: append(list, item) modifies in-place
- Use join() for string building in loops: result = "".join(parts)
- Error handling: try/except/finally, raise "message" or raise ValueError("msg")

RETURNING RESULTS:
- print() output is captured and returned automatically
- For structured data: import scriptling.mcp.tool; tool.return_object(data)
- For text: tool.return_string(text)
- Use help(topic) for built-in help: help("builtins"), help("json"), help("requests")`,
			mcp_lib.String("code", "Scriptling code to execute (Python 3-like syntax)", mcp_lib.Required()),
		),
		func(ctx context.Context, req *mcp_lib.ToolRequest) (*mcp_lib.ToolResponse, error) {
			code, _ := req.String("code")
			stm.logger.Trace("MCP execute_script invoked", "code_len", len(code))
			p := stm.newScriptling()

			response, exitCode, err := scriptlingmcp.RunToolScript(ctx, p, code, map[string]interface{}{})

			// If the script produced an explicit response (via return_error,
			// return_string, etc.), return it to the client. return_error sets a
			// response AND exits non-zero, so check for a response before treating
			// non-zero exit as a failure.
			if response != "" {
				if exitCode != 0 {
					stm.logger.Debug("MCP execute_script returned error response", "exit_code", exitCode)
					return nil, mcp_lib.NewToolErrorInternal(response)
				}
				stm.logger.Trace("MCP execute_script completed", "exit_code", exitCode, "response_len", len(response))
				return mcp_lib.NewToolResponseText(response), nil
			}

			if err != nil {
				stm.logger.Debug("MCP execute_script failed", "exit_code", exitCode, "error", err)
				return nil, fmt.Errorf("execution error: %w", err)
			}

			return mcp_lib.NewToolResponseText(""), nil
		},
	)
	stm.logger.Info("Registered MCP tool", "name", "execute_script", "params", 1, "mode", "native")
}

// and runs watchLoop to dispatch events. Resources and prompts use a tree
// structure (first path segment = URI scheme / namespace), so we watch
// recursively — every subdirectory is added. Tools are flat (single folder
// of .toml+.py pairs) so no recursion needed. New subdirectories created
// at runtime are picked up on the next reload (the watcher catches the
// mkdir event).
func (stm *scriptlingToolManager) startWatching() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	stm.watcher = watcher

	// Tools: flat watch (no subdirectories).
	if stm.toolsDirAbs != "" {
		if err := watcher.Add(stm.toolsDirAbs); err != nil {
			stm.logger.Warn("Failed to watch folder, auto-reload disabled for it", "kind", "tools", "path", stm.toolsDirAbs, "error", err)
		} else {
			stm.logger.Info("Watching folder for changes", "kind", "tools", "path", stm.toolsDirAbs)
		}
	}

	// Resources + prompts: recursive watch (subdirectories are part of
	// the URI scheme / namespace structure).
	for _, pair := range []struct {
		dir  string
		kind string
	}{
		{stm.resourcesDirAbs, "resources"},
		{stm.promptsDirAbs, "prompts"},
	} {
		if pair.dir == "" {
			continue
		}
		added := stm.watchRecursive(watcher, pair.dir)
		if added > 0 {
			stm.logger.Info("Watching folder tree for changes", "kind", pair.kind, "path", pair.dir, "dirs", added)
		} else {
			stm.logger.Warn("Failed to watch folder, auto-reload disabled for it", "kind", pair.kind, "path", pair.dir)
		}
	}

	stm.wg.Add(1)
	go stm.watchLoop()

	return nil
}

// watchRecursive walks dir and adds every subdirectory to the watcher.
// Returns the number of directories successfully added.
func (stm *scriptlingToolManager) watchRecursive(watcher *fsnotify.Watcher, dir string) int {
	count := 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil {
			stm.logger.Warn("Failed to watch subdirectory", "path", path, "error", err)
			return nil
		}
		count++
		return nil
	})
	return count
}

// watchLoop dispatches fsnotify events to the right per-kind reload path.
// Tools debounce per name (their TOML+.py pairs are independent); resources
// and prompts debounce as a group because their scan logic depends on sibling
// files. Only top-level files in each folder trigger reloads — fsnotify is
// non-recursive and a flat watch matches the scriptling-cli/server behaviour.
func (stm *scriptlingToolManager) watchLoop() {
	defer stm.wg.Done()

	for {
		select {
		case <-stm.done:
			return
		case event, ok := <-stm.watcher.Events:
			if !ok {
				return
			}
			stm.dispatchEvent(event)
		case _, ok := <-stm.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (stm *scriptlingToolManager) dispatchEvent(event fsnotify.Event) {
	dir := filepath.Dir(event.Name)
	ext := filepath.Ext(event.Name)
	isDelete := event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0

	switch {
	case stm.toolsDirAbs != "" && dir == stm.toolsDirAbs:
		if ext != ".toml" && ext != ".py" {
			return
		}
		toolName := strings.TrimSuffix(filepath.Base(event.Name), ext)
		stm.scheduleToolReload(toolName, isDelete)

	case stm.resourcesDirAbs != "" && strings.HasPrefix(event.Name, stm.resourcesDirAbs):
		// Resources use a tree structure (subdirs = URI scheme).
		// Any file change anywhere in the tree triggers a full reload.
		// New subdirectories also trigger (mkdir event → reload picks
		// them up via ScanResourcesTree).
		stm.scheduleResourcesReload()

	case stm.promptsDirAbs != "" && strings.HasPrefix(event.Name, stm.promptsDirAbs):
		// Prompts are flat (.toml+.py pairs or .md/.txt in the root),
		// but we watch recursively for consistency.
		if ext != ".toml" && ext != ".py" && ext != ".md" && ext != ".txt" {
			return
		}
		stm.schedulePromptsReload()
	}
}

// scheduleToolReload debounces a per-tool reload. Delete/Rename events remove
// the tool if both its .toml and .py are gone; Create/Write events re-register
// it if both files are present.
func (stm *scriptlingToolManager) scheduleToolReload(toolName string, isDelete bool) {
	stm.debounceMu.Lock()
	defer stm.debounceMu.Unlock()

	if t, ok := stm.toolTimers[toolName]; ok {
		t.Stop()
	}

	stm.toolTimers[toolName] = time.AfterFunc(stm.debounceDuration, func() {
		stm.debounceMu.Lock()
		delete(stm.toolTimers, toolName)
		stm.debounceMu.Unlock()

		if isDelete {
			stm.handleToolDelete(toolName)
		} else {
			stm.handleToolCreate(toolName)
		}
	})
}

func (stm *scriptlingToolManager) handleToolDelete(toolName string) {
	tomlPath := filepath.Join(stm.toolsDirAbs, toolName+".toml")
	pyPath := filepath.Join(stm.toolsDirAbs, toolName+".py")

	if fileExists(tomlPath) && fileExists(pyPath) {
		return
	}

	if stm.mainServer.UnregisterTool(toolName) {
		stm.logger.Info("Unregistered scriptling MCP tool", "name", toolName)
	}
}

func (stm *scriptlingToolManager) handleToolCreate(toolName string) {
	tomlPath := filepath.Join(stm.toolsDirAbs, toolName+".toml")
	pyPath := filepath.Join(stm.toolsDirAbs, toolName+".py")

	if !fileExists(tomlPath) || !fileExists(pyPath) {
		return
	}

	tools, err := mcpcli.ScanToolsFolder(stm.toolsDirAbs)
	if err != nil {
		stm.logger.Error("Failed to rescan tools folder", "error", err)
		return
	}
	meta, ok := tools[toolName]
	if !ok {
		return
	}
	stm.registerTool(toolName, meta)
}

func (stm *scriptlingToolManager) registerTool(toolName string, meta *toolmetadata.ToolMetadata) {
	scriptPath := filepath.Join(stm.toolsDirAbs, toolName+".py")

	tool, err := toolmetadata.BuildMCPTool(toolName, meta)
	if err != nil {
		stm.logger.Error("Failed to build tool", "tool", toolName, "error", err)
		return
	}
	handler, err := mcpcli.BuildToolHandler(scriptPath, stm.handlerCfg)
	if err != nil {
		stm.logger.Error("Failed to load tool handler", "tool", toolName, "error", err)
		return
	}

	mode := "native"
	if meta.Discoverable {
		mode = "discoverable"
	}
	stm.mainServer.RegisterTool(tool, handler)
	stm.logger.Info("Registered scriptling MCP tool", "name", toolName, "mode", mode)
}

// scheduleResourcesReload debounces a full resources reload: unregister every
// previously-registered static resource and template, rescan the folder, and
// re-register the lot. Notifies connected clients so they re-fetch.
func (stm *scriptlingToolManager) scheduleResourcesReload() {
	stm.debounceMu.Lock()
	defer stm.debounceMu.Unlock()

	if stm.resourceTimer != nil {
		stm.resourceTimer.Stop()
	}
	stm.resourceTimer = time.AfterFunc(stm.debounceDuration, stm.reloadResources)
}

func (stm *scriptlingToolManager) reloadResources() {
	stm.logger.Info("Reloading scriptling resources",
		"old_static", len(stm.resourceStaticURIs), "old_templates", len(stm.resourceTemplates))

	// Unregister everything we previously registered.
	for _, uri := range stm.resourceStaticURIs {
		stm.mainServer.UnregisterResource(uri)
	}
	for _, uriTmpl := range stm.resourceTemplates {
		stm.mainServer.UnregisterResourceTemplate(uriTmpl)
	}
	// Clear tracking BEFORE re-registering. registerResources may
	// partially succeed (register some, fail on others) — we always
	// update tracking with whatever was registered so the next reload
	// can clean up properly.
	stm.resourceStaticURIs = nil
	stm.resourceTemplates = nil

	staticURIs, templates, err := stm.registerResources()
	stm.resourceStaticURIs = staticURIs
	stm.resourceTemplates = templates
	if err != nil {
		stm.logger.Error("Failed to reload scriptling resources", "error", err)
	}
	stm.logger.Info("Resources reloaded",
		"new_static", len(staticURIs), "new_templates", len(templates))
	stm.mainServer.NotifyResourcesChanged()
	if stm.eventBroadcaster != nil { stm.eventBroadcaster.Broadcast(lmchatkit.ServerEvent{Type: "resources_changed"}) }
}

// registerResources scans the resources folder and registers every static
// resource and template on the live server. Returns the URIs registered so the
// caller can later unregister them.
func (stm *scriptlingToolManager) registerResources() (staticURIs, templates []string, err error) {
	entries, err := mcpcli.ScanResourcesTree(stm.resourcesDirAbs)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.Template {
			handler, err := mcpcli.BuildResourceScriptHandler(e.FilePath, e.MimeType, stm.handlerCfg)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to load resource template %s: %w", e.URI, err)
			}
			stm.mainServer.RegisterResourceTemplate(
				mcp_lib.NewResourceTemplate(e.URI, e.Name, e.Description, e.MimeType),
				handler,
			)
			templates = append(templates, e.URI)
			stm.logger.Info("Registered scriptling MCP resource template", "uri", e.URI)
		} else {
			handler := mcpcli.BuildStaticResourceHandler(mcpcli.FileReader(e.FilePath), e.URI, e.MimeType)
			stm.mainServer.RegisterResource(
				mcp_lib.NewResource(e.URI, e.Name, e.Description, e.MimeType),
				handler,
			)
			staticURIs = append(staticURIs, e.URI)
			stm.logger.Info("Registered scriptling MCP resource", "uri", e.URI)
		}
	}
	return staticURIs, templates, nil
}

// schedulePromptsReload debounces a full prompts reload.
func (stm *scriptlingToolManager) schedulePromptsReload() {
	stm.debounceMu.Lock()
	defer stm.debounceMu.Unlock()

	if stm.promptTimer != nil {
		stm.promptTimer.Stop()
	}
	stm.promptTimer = time.AfterFunc(stm.debounceDuration, stm.reloadPrompts)
}

func (stm *scriptlingToolManager) reloadPrompts() {
	for _, name := range stm.promptNames {
		stm.mainServer.UnregisterPrompt(name)
	}
	names, err := stm.registerPrompts()
	if err != nil {
		stm.logger.Error("Failed to reload scriptling prompts", "error", err)
	} else {
		stm.promptNames = names
	}
	stm.mainServer.NotifyPromptsChanged()
	if stm.eventBroadcaster != nil { stm.eventBroadcaster.Broadcast(lmchatkit.ServerEvent{Type: "prompts_changed"}) }
}

// registerPrompts scans the prompts folder and registers every prompt on the
// live server. Returns the names registered so the caller can later unregister
// them.
func (stm *scriptlingToolManager) registerPrompts() ([]string, error) {
	entries, err := mcpcli.ScanPromptsFolder(stm.promptsDirAbs)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		var handler mcp_lib.PromptHandler
		if e.Static {
			handler = mcpcli.BuildStaticPromptHandler(mcpcli.FileReader(e.FilePath))
		} else {
			h, err := mcpcli.BuildPromptScriptHandler(e.FilePath, stm.handlerCfg)
			if err != nil {
				return nil, fmt.Errorf("failed to load prompt %s: %w", e.Name, err)
			}
			handler = h
		}
		builder := mcp_lib.NewPrompt(e.Name, e.Description)
		for _, arg := range e.Arguments {
			builder.Argument(arg.Name, arg.Description, arg.Required)
		}
		stm.mainServer.RegisterPrompt(builder, handler)
		names = append(names, e.Name)
		mode := "static"
		if !e.Static {
			mode = "dynamic"
		}
		stm.logger.Info("Registered scriptling MCP prompt", "prompt", e.Name, "mode", mode, "args", len(e.Arguments))
	}
	return names, nil
}

// Shutdown stops the watcher, cancels pending debounced reloads, closes plugins,
// and blocks until the watch loop has exited.
func (stm *scriptlingToolManager) Shutdown() {
	close(stm.done)
	if stm.watcher != nil {
		stm.watcher.Close()
	}
	stm.debounceMu.Lock()
	for _, t := range stm.toolTimers {
		t.Stop()
	}
	if stm.resourceTimer != nil {
		stm.resourceTimer.Stop()
	}
	if stm.promptTimer != nil {
		stm.promptTimer.Stop()
	}
	stm.debounceMu.Unlock()
	if stm.plugins != nil {
		stm.plugins.Close()
	}
	stm.wg.Wait()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
