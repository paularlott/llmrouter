package router

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	scriptling "github.com/paularlott/scriptling"
	"github.com/paularlott/scriptling/extlibs"
	"github.com/paularlott/scriptling/extlibs/agent"
	"github.com/paularlott/scriptling/extlibs/ai"
	"github.com/paularlott/scriptling/extlibs/fuzzy"
	"github.com/paularlott/scriptling/extlibs/mcp"
	"github.com/paularlott/scriptling/object"
	"github.com/paularlott/scriptling/stdlib"
)

const (
	reqTypeChat      = "chat"
	reqTypeResponses = "responses"

	vmPoolSize = 5
)

// SmartRouter runs the routing script using a pool of pre-warmed VMs.
// The pool is rebuilt when the script or any library in libdir changes on disk.
type SmartRouter struct {
	scriptPath   string
	defaultModel string
	vars         map[string]string
	libDir       string
	router       *Router
	logger       Logger

	mu        sync.RWMutex
	scriptSrc string
	scriptLibs map[string]string // name -> source, loaded from libdir
	pool      chan *scriptling.Scriptling // buffered channel as VM pool
	watcher   *fsnotify.Watcher
	stopCh    chan struct{}
}

func newSmartRouter(scriptPath, defaultModel string, vars map[string]string, libDir string, r *Router, logger Logger) (*SmartRouter, error) {
	sr := &SmartRouter{
		scriptPath:   scriptPath,
		defaultModel: defaultModel,
		vars:         vars,
		libDir:       libDir,
		router:       r,
		logger:       logger,
		scriptLibs:   make(map[string]string),
		stopCh:       make(chan struct{}),
	}

	if libDir != "" {
		if err := sr.loadLibDir(); err != nil {
			logger.Warn("smart routing libdir load failed", "error", err)
		}
	}

	if err := sr.loadScript(); err != nil {
		logger.Warn("smart routing script load failed, will use default model", "error", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	sr.watcher = watcher

	if scriptPath != "" {
		if err := watcher.Add(scriptPath); err != nil {
			logger.Warn("could not watch routing script", "path", scriptPath, "error", err)
		}
	}
	if libDir != "" {
		if err := watcher.Add(libDir); err != nil {
			logger.Warn("could not watch routing libdir", "path", libDir, "error", err)
		}
	}

	go sr.watchLoop()
	return sr, nil
}

// loadLibDir reads all .py files from libdir and stores them as named script libraries.
func (sr *SmartRouter) loadLibDir() error {
	entries, err := os.ReadDir(sr.libDir)
	if err != nil {
		return err
	}
	libs := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sr.libDir, e.Name()))
		if err != nil {
			sr.logger.Warn("failed to read script library", "file", e.Name(), "error", err)
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".py")
		libs[name] = string(data)
	}
	sr.mu.Lock()
	sr.scriptLibs = libs
	sr.mu.Unlock()
	sr.logger.Info("routing script libraries loaded", "path", sr.libDir, "count", len(libs))
	return nil
}

// newVM creates and fully initialises a single Scriptling VM with all libraries registered.
func (sr *SmartRouter) newVM() *scriptling.Scriptling {
	vm := scriptling.New()
	stdlib.RegisterAll(vm)
	extlibs.RegisterRequestsLibrary(vm)
	extlibs.RegisterSecretsLibrary(vm)
	extlibs.RegisterHTMLParserLibrary(vm)
	extlibs.RegisterLoggingLibraryDefault(vm)
	extlibs.RegisterYAMLLibrary(vm)
	extlibs.RegisterTOMLLibrary(vm)
	extlibs.RegisterRuntimeLibrary(vm)
	extlibs.RegisterRuntimeKVLibrary(vm)
	extlibs.RegisterRuntimeSyncLibrary(vm)
	ai.Register(vm)
	if err := agent.Register(vm); err != nil {
		sr.logger.Warn("failed to register scriptling.ai.agent", "error", err)
	}
	mcp.Register(vm)
	mcp.RegisterToon(vm)
	fuzzy.Register(vm)

	if len(sr.vars) > 0 {
		pairs := make(map[string]object.Object, len(sr.vars))
		for k, v := range sr.vars {
			pairs[k] = &object.String{Value: v}
		}
		vm.RegisterLibrary(object.NewLibrary("vars", nil, pairs, "User-defined variables from smart_routing config"))
	}

	vm.RegisterLibrary(buildRouterLibrary(sr.router))

	for name, src := range sr.scriptLibs {
		if err := vm.RegisterScriptLibrary(name, src); err != nil {
			sr.logger.Warn("failed to register script library", "name", name, "error", err)
		}
	}

	return vm
}

// buildPool creates a fresh pool of vmPoolSize pre-warmed VMs.
func (sr *SmartRouter) buildPool() chan *scriptling.Scriptling {
	pool := make(chan *scriptling.Scriptling, vmPoolSize)
	for i := 0; i < vmPoolSize; i++ {
		pool <- sr.newVM()
	}
	return pool
}

func (sr *SmartRouter) loadScript() error {
	if sr.scriptPath == "" {
		return nil
	}
	data, err := os.ReadFile(sr.scriptPath)
	if err != nil {
		return err
	}
	newPool := sr.buildPool()
	sr.mu.Lock()
	sr.scriptSrc = string(data)
	sr.pool = newPool
	sr.mu.Unlock()
	sr.logger.Info("routing script loaded", "path", sr.scriptPath)
	return nil
}

func (sr *SmartRouter) watchLoop() {
	var debounce <-chan time.Time
	for {
		select {
		case <-sr.stopCh:
			sr.watcher.Close()
			return
		case event, ok := <-sr.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
				debounce = time.After(100 * time.Millisecond)
			}
		case <-debounce:
			// Reload libdir first (if configured and the changed file is in it)
			if sr.libDir != "" {
				if err := sr.loadLibDir(); err != nil {
					sr.logger.Warn("routing libdir reload failed", "error", err)
				} else {
					sr.logger.Debug("routing libdir reloaded", "path", sr.libDir)
				}
			}
			if err := sr.loadScript(); err != nil {
				sr.logger.Warn("routing script reload failed", "error", err)
			} else {
				sr.logger.Debug("routing script reloaded", "path", sr.scriptPath)
			}
			debounce = nil
		case err, ok := <-sr.watcher.Errors:
			if !ok {
				return
			}
			sr.logger.Warn("routing script watcher error", "error", err)
		}
	}
}

func (sr *SmartRouter) Stop() {
	close(sr.stopCh)
}

// RouteResult holds the model and optional provider hint from the routing script.
type RouteResult struct {
	Model        string
	ProviderHint string
}

func (sr *SmartRouter) Route(ctx context.Context, req *ChatCompletionRequest) RouteResult {
	return sr.route(ctx, req, nil)
}

func (sr *SmartRouter) RouteResponse(ctx context.Context, req *CreateResponseRequest) RouteResult {
	return sr.route(ctx, nil, req)
}

func (sr *SmartRouter) route(ctx context.Context, chatReq *ChatCompletionRequest, respReq *CreateResponseRequest) RouteResult {
	sr.mu.RLock()
	src := sr.scriptSrc
	pool := sr.pool
	sr.mu.RUnlock()

	if src == "" {
		return RouteResult{Model: sr.defaultModel}
	}

	var reqType string
	var inputData map[string]interface{}
	if chatReq != nil {
		reqType = reqTypeChat
		inputData = map[string]interface{}{
			"type":     reqType,
			"messages": messagesForScript(chatReq.Messages),
			"tools":    toolsForScript(chatReq.Tools),
		}
	} else {
		reqType = reqTypeResponses
		inputData = map[string]interface{}{
			"type":  reqType,
			"model": respReq.Model,
		}
	}
	reqJSON, _ := json.Marshal(inputData)

	// Borrow a VM from the pool (block until one is available or ctx is done)
	var vm *scriptling.Scriptling
	select {
	case vm = <-pool:
	case <-ctx.Done():
		return RouteResult{Model: sr.defaultModel}
	}

	// Inject per-request data as variables
	_ = vm.SetVar("request_json", string(reqJSON))

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, evalErr := vm.EvalWithContext(timeoutCtx, src)

	// Read output before reset
	selectedModel, _ := vm.GetVarAsString("output_model")
	providerHint, _ := vm.GetVarAsString("output_provider")

	// Reset env: keep only import builtin and registered lib dicts
	vm.ResetEnv("vars", "router")

	// Return VM to pool
	pool <- vm

	if evalErr != nil {
		sr.logger.Warn("smart routing script error", "error", evalErr)
		return RouteResult{Model: sr.defaultModel}
	}

	if selectedModel == "" {
		return RouteResult{Model: sr.defaultModel}
	}

	sr.router.ModelMapMu.RLock()
	_, ok := sr.router.ModelMap[selectedModel]
	sr.router.ModelMapMu.RUnlock()

	if !ok {
		sr.logger.Warn("smart routing returned unknown model, using default", "model", selectedModel)
		return RouteResult{Model: sr.defaultModel}
	}

	sr.logger.Debug("smart routing resolved", "model", selectedModel, "provider_hint", providerHint)
	return RouteResult{Model: selectedModel, ProviderHint: providerHint}
}

func messageContentTypes(msgs []Message) []string {
	seen := make(map[string]struct{})
	for _, m := range msgs {
		switch c := m.Content.(type) {
		case string:
			seen["text"] = struct{}{}
		case []any:
			for _, part := range c {
				if p, ok := part.(map[string]any); ok {
					if t, ok := p["type"].(string); ok {
						seen[t] = struct{}{}
					}
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	return result
}

func messagesForScript(msgs []Message) []interface{} {
	out := make([]interface{}, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
	}
	return out
}

func toolsForScript(tools []Tool) []interface{} {
	out := make([]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"type": t.Type,
			"name": t.Function.Name,
		}
	}
	return out
}
