package router

import (
	"context"
	"encoding/json"
	"os"
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
)

// SmartRouter runs the routing script and hot-reloads it on file changes.
type SmartRouter struct {
	scriptPath   string
	defaultModel string
	vars         map[string]string
	router       *Router
	logger       Logger

	mu        sync.RWMutex
	scriptSrc string
	watcher   *fsnotify.Watcher
	stopCh    chan struct{}
}

func newSmartRouter(scriptPath, defaultModel string, vars map[string]string, r *Router, logger Logger) (*SmartRouter, error) {
	sr := &SmartRouter{
		scriptPath:   scriptPath,
		defaultModel: defaultModel,
		vars:         vars,
		router:       r,
		logger:       logger,
		stopCh:       make(chan struct{}),
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

	go sr.watchLoop()
	return sr, nil
}

func (sr *SmartRouter) loadScript() error {
	if sr.scriptPath == "" {
		return nil
	}
	data, err := os.ReadFile(sr.scriptPath)
	if err != nil {
		return err
	}
	sr.mu.Lock()
	sr.scriptSrc = string(data)
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
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				debounce = time.After(100 * time.Millisecond)
			}
		case <-debounce:
			if err := sr.loadScript(); err != nil {
				sr.logger.Warn("routing script reload failed", "error", err)
			} else {
				sr.logger.Debug("routing script content loaded", "path", sr.scriptPath, "bytes", len(sr.scriptSrc))
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

// Route runs the routing script and returns the resolved model and optional provider hint.
func (sr *SmartRouter) Route(ctx context.Context, req *ChatCompletionRequest) RouteResult {
	return sr.route(ctx, req, nil)
}

// RouteResponse is like Route but for a Responses API request.
func (sr *SmartRouter) RouteResponse(ctx context.Context, req *CreateResponseRequest) RouteResult {
	return sr.route(ctx, nil, req)
}

func (sr *SmartRouter) route(ctx context.Context, chatReq *ChatCompletionRequest, respReq *CreateResponseRequest) RouteResult {
	sr.mu.RLock()
	src := sr.scriptSrc
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

	// selectedModel and providerHint are set by router.set_model() inside the script
	var selectedModel, providerHint string
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

	// Expose configured vars to the script
	if len(sr.vars) > 0 {
		pairs := make(map[string]object.Object, len(sr.vars))
		for k, v := range sr.vars {
			pairs[k] = &object.String{Value: v}
		}
		vm.RegisterLibrary(object.NewLibrary("vars", nil, pairs, "User-defined variables from smart_routing config"))
	}

	var msgs []Message
	if chatReq != nil {
		msgs = chatReq.Messages
	}
	vm.RegisterLibrary(buildRouterLibraryForRequest(sr.router, string(reqJSON), reqType, msgs, func(m, hint string) {
		selectedModel = m
		providerHint = hint
	}))

	if err := vm.SetVar("request_json", string(reqJSON)); err != nil {
		sr.logger.Warn("smart routing: failed to set request", "error", err)
		return RouteResult{Model: sr.defaultModel}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := vm.EvalWithContext(timeoutCtx, src); err != nil {
		sr.logger.Warn("smart routing script error", "error", err)
		return RouteResult{Model: sr.defaultModel}
	}

	// set_model() takes priority; fall back to output_model variable
	if selectedModel == "" {
		if m, _ := vm.GetVar("output_model"); m != nil {
			if s, ok := m.(string); ok {
				selectedModel = s
			}
		}
	}

	if selectedModel == "" {
		return RouteResult{Model: sr.defaultModel}
	}

	// Validate model exists
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
