package router

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
	scriptling "github.com/paularlott/scriptling"
	"github.com/paularlott/scriptling/extlibs"
	"github.com/paularlott/scriptling/extlibs/agent"
	"github.com/paularlott/scriptling/extlibs/ai"
	"github.com/paularlott/scriptling/extlibs/mcp"
	"github.com/paularlott/scriptling/extlibs/net/resolve"
	"github.com/paularlott/scriptling/extlibs/similarity"
	"github.com/paularlott/scriptling/libloader"
	"github.com/paularlott/scriptling/object"
	"github.com/paularlott/scriptling/stdlib"
	"github.com/paularlott/llmrouter/internal/types"
)

const (
	reqTypeChat      = "chat"
	reqTypeResponses = "responses"

	vmPoolSize = 5
)

// SmartRouter runs a single routing script using a pool of pre-warmed VMs.
// It is identified by name: clients request that model name and the script runs.
// The pool is rebuilt by SmartRouterManager when the script, config, or libraries change.
type SmartRouter struct {
	name         string // trigger model name (clients request this)
	scriptPath   string // <folder>/<name>.py; empty when the router is an alias-only router
	defaultModel string
	vars         map[string]string
	libDirs      []string
	router       *Router
	logger       Logger

	mu        sync.RWMutex
	scriptSrc string
	pool      chan *scriptling.Scriptling // buffered channel as VM pool
	sig       string                      // signature of on-disk config+script, for change detection
	stopCh    chan struct{}
}

func newSmartRouter(name, scriptPath, defaultModel string, vars map[string]string, libDirs []string, r *Router, logger Logger) (*SmartRouter, error) {
	sr := &SmartRouter{
		name:         name,
		scriptPath:   scriptPath,
		defaultModel: defaultModel,
		vars:         vars,
		libDirs:      libDirs,
		router:       r,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}

	if err := sr.loadScript(); err != nil {
		logger.Warn("smart routing script load failed, will use default model", "router", name, "error", err)
	}

	return sr, nil
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
	similarity.Register(vm)
	resolve.Register(vm, dnsResolver{timeout: 2 * time.Second})
	extlibs.RegisterTemplateHTMLLibrary(vm)
	extlibs.RegisterTemplateTextLibrary(vm)

	// vars library: each config var is exposed as an attribute (vars.key) plus a
	// get(name, default="") function for dynamic lookup. Always registered so
	// vars.get() exists even when no vars are defined.
	//
	// The constants are a build-time snapshot of sr.vars; the get() function reads
	// sr.vars live under the lock. No lock is taken here because vars is replaced
	// wholesale (never mutated in place) and pool builds are never concurrent with
	// a replacement (the manager serialises reconfigure/rebuildPool).
	vb := object.NewLibraryBuilder("vars", "User-defined variables from the smart router config")
	for k, v := range sr.vars {
		vb.Constant(k, v)
	}
	vb.FunctionWithHelp("get", func(kwargs object.Kwargs, name string) string {
		sr.mu.RLock()
		v, ok := sr.vars[name]
		sr.mu.RUnlock()
		if !ok {
			return kwargs.MustGetString("default", "")
		}
		return v
	}, "get(name, default='') -> str - Look up a config variable by name")
	vm.RegisterLibrary(vb.Build())

	vm.RegisterLibrary(buildRouterLibrary(sr.router))

	// Set up library loader for dynamic loading from libDirs
	// Supports Python-style folder structure: knot/groups.py → import knot.groups
	// Directories are searched in order: script dir first, then any additional libpath entries
	if len(sr.libDirs) > 0 {
		loader := libloader.NewMultiFilesystem(sr.libDirs...)
		vm.SetLibraryLoader(loader)
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

func (sr *SmartRouter) Stop() {
	select {
	case <-sr.stopCh:
	default:
		close(sr.stopCh)
	}
}

// reconfigure swaps in a new script path/config (from a changed .toml/.py) and
// rebuilds the pool. Vars are baked into VMs at build time, so any vars change
// requires a pool rebuild.
func (sr *SmartRouter) reconfigure(scriptPath, defaultModel string, vars map[string]string) {
	newSrc := ""
	if scriptPath != "" {
		if data, err := os.ReadFile(scriptPath); err == nil {
			newSrc = string(data)
		} else {
			sr.logger.Warn("smart routing script reload failed", "router", sr.name, "path", scriptPath, "error", err)
		}
	}
	sr.mu.Lock()
	sr.scriptPath = scriptPath
	sr.scriptSrc = newSrc
	sr.defaultModel = defaultModel
	sr.vars = vars
	sr.mu.Unlock()
	pool := sr.buildPool()
	sr.mu.Lock()
	sr.pool = pool
	sr.mu.Unlock()
}

// rebuildPool builds a fresh VM pool and swaps it in (used when a shared library changes).
func (sr *SmartRouter) rebuildPool() {
	pool := sr.buildPool()
	sr.mu.Lock()
	sr.pool = pool
	sr.mu.Unlock()
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

// smartRouterFor returns the smart router registered under the given model name,
// or nil if smart routing is disabled or no router matches.
func (r *Router) smartRouterFor(model string) *SmartRouter {
	if r.smartRouters == nil {
		return nil
	}
	return r.smartRouters.get(model)
}

// smartRouterNames returns the trigger model names of all registered smart routers.
func (r *Router) smartRouterNames() []string {
	if r.smartRouters == nil {
		return nil
	}
	return r.smartRouters.names()
}

// SmartRouterManager owns the set of SmartRouters discovered from a folder of
// <model>.toml (+ optional <model>.py) pairs. It watches the folder and the
// global library path, adding/removing/rebuilding routers as files change.
//
// The folder is always the first library search path (so shared .py libs placed
// alongside routers are importable by all of them), followed by the global
// libpath directories.
type SmartRouterManager struct {
	folder        string
	routerLibDirs []string // [folder] + globalLibDirs, passed to each router
	router        *Router
	logger        Logger

	mu      sync.RWMutex
	routers map[string]*SmartRouter

	watcher *fsnotify.Watcher
	stopCh  chan struct{}
}

func newSmartRouterManager(folder string, globalLibDirs []string, r *Router, logger Logger) (*SmartRouterManager, error) {
	folder = strings.TrimRight(folder, string(filepath.Separator))
	libDirs := make([]string, 0, 1+len(globalLibDirs))
	libDirs = append(libDirs, folder)
	libDirs = append(libDirs, globalLibDirs...)
	return &SmartRouterManager{
		folder:        folder,
		routerLibDirs: libDirs,
		router:        r,
		logger:        logger,
		routers:       make(map[string]*SmartRouter),
		stopCh:        make(chan struct{}),
	}, nil
}

// Start performs the initial scan and begins watching the folder and libpath dirs.
func (m *SmartRouterManager) Start() error {
	if err := m.scan(false); err != nil {
		m.logger.Warn("smart-router initial scan failed", "folder", m.folder, "error", err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.watcher = watcher
	if err := watcher.Add(m.folder); err != nil {
		m.logger.Warn("could not watch smart-router folder, live reload disabled", "path", m.folder, "error", err)
	}
	for _, dir := range m.routerLibDirs {
		if dir == "" || dir == m.folder {
			continue
		}
		if err := watcher.Add(dir); err != nil {
			m.logger.Warn("could not watch smart-router libpath dir, live reload disabled for it", "path", dir, "error", err)
		}
	}
	go m.watchLoop()
	return nil
}

func (m *SmartRouterManager) Stop() {
	select {
	case <-m.stopCh:
		return
	default:
		close(m.stopCh)
	}
	if m.watcher != nil {
		m.watcher.Close()
	}
	m.mu.Lock()
	for _, sr := range m.routers {
		sr.Stop()
	}
	m.routers = nil
	m.mu.Unlock()
}

func (m *SmartRouterManager) get(name string) *SmartRouter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.routers[name]
}

func (m *SmartRouterManager) names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.routers))
	for n := range m.routers {
		out = append(out, n)
	}
	return out
}

// reconcileCollisions removes any router whose name is now served by a real
// provider model, so smart-router names never shadow a provider model. Called
// after model discovery refreshes the model map.
func (m *SmartRouterManager) reconcileCollisions(modelMap map[string][]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, sr := range m.routers {
		if _, ok := modelMap[name]; ok {
			m.logger.Warn("smart router name collides with a provider model, disabling router", "router", name)
			sr.Stop()
			delete(m.routers, name)
		}
	}
}

// desiredRouter holds the parsed configuration for one router discovered on disk.
type desiredRouter struct {
	defaultModel string
	vars         map[string]string
	scriptPath   string // empty when no <name>.py exists (alias-only router)
	sig          string
}

// scan reads the folder, parses each <model>.toml, and reconciles the router
// set: adding new routers, removing gone ones, and reloading those whose config
// or script changed. When forceRebuild is true every surviving router's pool is
// rebuilt (used after a shared-library change).
func (m *SmartRouterManager) scan(forceRebuild bool) error {
	entries, err := os.ReadDir(m.folder)
	if err != nil {
		return err
	}

	desired := make(map[string]desiredRouter)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || filepath.Ext(name) != ".toml" {
			continue
		}
		stem := strings.TrimSuffix(name, ".toml")
		if !validRouterName(stem) {
			m.logger.Warn("skipping smart-router file: invalid router name", "file", name)
			continue
		}
		tomlPath := filepath.Join(m.folder, name)
		var cfg types.RouterFileConfig
		if _, err := toml.DecodeFile(tomlPath, &cfg); err != nil {
			m.logger.Warn("skipping smart-router file: invalid TOML", "file", name, "error", err)
			continue
		}
		if !cfg.Enabled {
			continue
		}
		scriptPath := ""
		pyPath := filepath.Join(m.folder, stem+".py")
		if fi, err := os.Stat(pyPath); err == nil && !fi.IsDir() {
			scriptPath = pyPath
		}
		desired[stem] = desiredRouter{
			defaultModel: cfg.DefaultModel,
			vars:         cfg.Vars,
			scriptPath:   scriptPath,
			sig:          signatureFor(tomlPath, scriptPath),
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// remove routers no longer present (or disabled)
	for name, sr := range m.routers {
		if _, ok := desired[name]; !ok {
			m.logger.Info("removed smart router", "router", name)
			sr.Stop()
			delete(m.routers, name)
		}
	}

	// refuse routers whose name collides with a real provider model
	m.router.ModelMapMu.RLock()
	for name := range desired {
		if _, ok := m.router.ModelMap[name]; ok {
			m.logger.Warn("smart router name collides with a provider model, skipping", "router", name)
			delete(desired, name)
		}
	}
	m.router.ModelMapMu.RUnlock()

	// add or update
	for name, d := range desired {
		if sr, ok := m.routers[name]; ok {
			if sr.sig != d.sig {
				m.logger.Info("reloading smart router", "router", name)
				sr.reconfigure(d.scriptPath, d.defaultModel, d.vars)
				sr.sig = d.sig
			} else if forceRebuild {
				sr.rebuildPool()
			}
			continue
		}
		sr, err := newSmartRouter(name, d.scriptPath, d.defaultModel, d.vars, m.routerLibDirs, m.router, m.logger)
		if err != nil {
			m.logger.Warn("failed to create smart router", "router", name, "error", err)
			continue
		}
		sr.sig = d.sig
		m.routers[name] = sr
		m.logger.Info("loaded smart router", "router", name, "script", d.scriptPath, "default_model", d.defaultModel)
	}
	return nil
}

func (m *SmartRouterManager) watchLoop() {
	var debounce <-chan time.Time
	var folderDirty, libDirty bool
	for {
		select {
		case <-m.stopCh:
			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
				continue
			}
			if m.isFolderPath(event.Name) {
				folderDirty = true
			} else {
				libDirty = true
			}
			debounce = time.After(100 * time.Millisecond)
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			m.logger.Warn("smart-router watcher error", "error", err)
		case <-debounce:
			if folderDirty || libDirty {
				if err := m.scan(libDirty); err != nil {
					m.logger.Warn("smart-router rescan failed", "error", err)
				}
			}
			folderDirty, libDirty = false, false
			debounce = nil
		}
	}
}

// isFolderPath reports whether p is inside the routers folder.
func (m *SmartRouterManager) isFolderPath(p string) bool {
	dir := filepath.Dir(p)
	rel, err := filepath.Rel(m.folder, dir)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// signatureFor returns a change signature built from file modification times.
func signatureFor(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(&b, "ERR|")
			continue
		}
		fmt.Fprintf(&b, "%d|", fi.ModTime().UnixNano())
	}
	return b.String()
}

// validRouterName reports whether stem is an acceptable router (model) name.
// Names must be a single path segment (no slashes) so they cannot collide with
// namespaced provider models and map cleanly to filenames.
func validRouterName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}
