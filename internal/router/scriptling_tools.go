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
	"github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
	"github.com/paularlott/llmrouter/internal/types"
	mcp_lib "github.com/paularlott/mcp"
	"github.com/paularlott/mcp/toolmetadata"
	"github.com/paularlott/scriptling"
	scriptlingmcp "github.com/paularlott/scriptling/extlibs/mcp"
	"github.com/paularlott/scriptling/extlibs/secretprovider"
	"github.com/paularlott/scriptling/libloader"
	scriptlingplugin "github.com/paularlott/scriptling/plugin"
	"github.com/paularlott/scriptling/scriptling-cli/setup"
)

type scriptlingToolManager struct {
	logger           Logger
	config           types.ScriptingConfig
	toolsDirAbs      string
	watcher          *fsnotify.Watcher
	plugins          *scriptlingplugin.Manager
	debounceDuration time.Duration
	debounceTimers   map[string]*time.Timer
	debounceMu       sync.Mutex
	done             chan struct{}
	wg               sync.WaitGroup
	mainServer       *mcp_lib.Server
}

func NewScriptlingToolManager(config types.ScriptingConfig, mainServer *mcp_lib.Server, logger Logger) (*scriptlingToolManager, error) {
	stm := &scriptlingToolManager{
		config:           config,
		logger:           logger,
		mainServer:       mainServer,
		debounceDuration: 500 * time.Millisecond,
		done:             make(chan struct{}),
		debounceTimers:   make(map[string]*time.Timer),
	}

	if config.ToolsDir != "" {
		abs, err := filepath.Abs(config.ToolsDir)
		if err != nil {
			abs = config.ToolsDir
		}
		stm.toolsDirAbs = abs
	}

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

	if err := stm.setupMCP(); err != nil {
		return nil, fmt.Errorf("failed to setup MCP server: %w", err)
	}

	if stm.toolsDirAbs != "" {
		if err := stm.startWatching(); err != nil {
			logger.Warn("Failed to start tools watcher, auto-reload disabled", "error", err)
		}
	}

	return stm, nil
}

func (stm *scriptlingToolManager) setupMCP() error {
	if stm.toolsDirAbs == "" {
		return nil
	}

	tools, err := scanToolsFolder(stm.toolsDirAbs)
	if err != nil {
		return fmt.Errorf("failed to scan tools folder %s: %w", stm.toolsDirAbs, err)
	}

	for toolName, meta := range tools {
		stm.registerTool(toolName, meta)
	}

	return nil
}

func (stm *scriptlingToolManager) startWatching() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	stm.watcher = watcher

	if err := watcher.Add(stm.toolsDirAbs); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch tools folder: %w", err)
	}

	stm.logger.Info("Watching tools folder for changes", "path", stm.toolsDirAbs)

	stm.wg.Add(1)
	go stm.watchLoop()

	return nil
}

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
			if filepath.Dir(event.Name) != stm.toolsDirAbs {
				continue
			}

			ext := filepath.Ext(event.Name)
			if ext != ".toml" && ext != ".py" {
				continue
			}

			toolName := strings.TrimSuffix(filepath.Base(event.Name), ext)

			if event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0 {
				stm.scheduleToolReload(toolName, true)
			} else if event.Op&fsnotify.Create != 0 || event.Op&fsnotify.Write != 0 {
				stm.scheduleToolReload(toolName, false)
			}

		case _, ok := <-stm.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (stm *scriptlingToolManager) scheduleToolReload(toolName string, isDelete bool) {
	stm.debounceMu.Lock()
	defer stm.debounceMu.Unlock()

	if t, ok := stm.debounceTimers[toolName]; ok {
		t.Stop()
	}

	stm.debounceTimers[toolName] = time.AfterFunc(stm.debounceDuration, func() {
		stm.debounceMu.Lock()
		delete(stm.debounceTimers, toolName)
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

	meta, err := loadToolMetadata(tomlPath, stm.toolsDirAbs)
	if err != nil {
		stm.logger.Error("Failed to load tool metadata", "tool", toolName, "error", err)
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
	handler, err := createMCPToolHandler(scriptPath, stm.config.LibPaths, stm.plugins)
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

func (stm *scriptlingToolManager) Shutdown() {
	close(stm.done)
	if stm.watcher != nil {
		stm.watcher.Close()
	}
	stm.debounceMu.Lock()
	for _, t := range stm.debounceTimers {
		t.Stop()
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

func scanToolsFolder(toolsFolder string) (map[string]*toolmetadata.ToolMetadata, error) {
	tools := make(map[string]*toolmetadata.ToolMetadata)

	entries, err := os.ReadDir(toolsFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to read tools folder: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}

		toolName := strings.TrimSuffix(entry.Name(), ".toml")
		tomlPath := filepath.Join(toolsFolder, entry.Name())

		meta, err := loadToolMetadata(tomlPath, toolsFolder)
		if err != nil {
			return nil, err
		}

		tools[toolName] = meta
	}

	return tools, nil
}

func loadToolMetadata(tomlPath string, toolsFolder string) (*toolmetadata.ToolMetadata, error) {
	baseConfig := cli_toml.NewConfigFile(&tomlPath, func() []string { return []string{toolsFolder} })
	cfg := cli.NewTypedConfigFile(baseConfig)
	if err := cfg.LoadData(); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", tomlPath, err)
	}

	meta := &toolmetadata.ToolMetadata{
		Description:  cfg.GetString("description"),
		Keywords:     cfg.GetStringSlice("keywords"),
		Discoverable: cfg.GetBool("discoverable"),
	}

	paramObjs := cfg.GetObjectSlice("parameters")
	for _, paramObj := range paramObjs {
		param := toolmetadata.ToolParameter{
			Name:        paramObj.GetString("name"),
			Type:        paramObj.GetString("type"),
			Description: paramObj.GetString("description"),
			Required:    paramObj.GetBool("required"),
		}
		meta.Parameters = append(meta.Parameters, param)
	}

	return meta, nil
}

func createMCPToolHandler(scriptPath string, libPaths []string, pluginManager *scriptlingplugin.Manager) (func(context.Context, *mcp_lib.ToolRequest) (*mcp_lib.ToolResponse, error), error) {
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script %s: %w", scriptPath, err)
	}

	scriptDir := filepath.Dir(scriptPath)
	toolLibDirs := append([]string{scriptDir}, libPaths...)

	handler := func(ctx context.Context, req *mcp_lib.ToolRequest) (*mcp_lib.ToolResponse, error) {
		params := req.Args()

		p := scriptling.New()

		setup.Scriptling(p, toolLibDirs, false, nil, nil, secretprovider.NewRegistry(), nil, "", "")

		scriptlingmcp.Register(p)
		scriptlingmcp.RegisterToolHelpers(p)

		scriptlingplugin.RegisterLibraries(p, pluginManager)

		p.SetLibraryLoader(libloader.NewMultiFilesystem(toolLibDirs...))

		response, exitCode, runErr := scriptlingmcp.RunToolScript(ctx, p, string(script), params)

		if response != "" {
			if exitCode != 0 {
				return nil, mcp_lib.NewToolErrorInternal(response)
			}
			return mcp_lib.NewToolResponseText(response), nil
		}

		if runErr != nil {
			return nil, fmt.Errorf("script execution failed: %w", runErr)
		}

		if exitCode != 0 {
			return nil, fmt.Errorf("script exited with code %d", exitCode)
		}

		return mcp_lib.NewToolResponseText(""), nil
	}

	return handler, nil
}
