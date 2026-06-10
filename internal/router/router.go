package router

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/conversations"
	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/llmrouter/middleware"
	"github.com/paularlott/mcp/ai/claude"
	"github.com/paularlott/mcp/ai/openai"
)

func NewRouter(config *types.Config, logger Logger) (*Router, error) {
	router := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		ModelTags:    make(map[string][]string),
		config:       config,
		logger:       logger,
		traceEnabled: strings.EqualFold(config.Logging.Level, "trace"),
		shutdownChan: make(chan struct{}),
	}

	// Initialize providers
	for _, providerConfig := range config.Providers {
		if !providerConfig.Enabled {
			continue
		}

		client, err := newAIClient(&providerConfig)
		if err != nil {
			logger.Warn("failed to create client for provider", "name", providerConfig.Name, "error", err)
			continue
		}

		weight := providerConfig.Weight
		if weight <= 0 {
			weight = 1.0
		}

		providerType := providerConfig.Provider
		if providerType == "" {
			providerType = "openai"
		}
		provider := &Provider{
			Name:           providerConfig.Name,
			ProviderType:   providerType,
			Enabled:        providerConfig.Enabled,
			Client:         client,
			Models:         providerConfig.Models,
			ModelAllowlist: providerConfig.ModelAllowlist,
			ModelDenylist:  providerConfig.ModelDenylist,
			Weight:         weight,
			Tags:           providerConfig.Tags,
			ModelTags:      providerConfig.ModelTags,
			ModelAliases:   providerConfig.ModelAliases,
		}
		provider.Healthy.Store(true)

		router.Providers[provider.Name] = provider
		logger.Info("initialized provider", "name", provider.Name, "type", provider.ProviderType)
	}

	// Initialize MCP server
	mcpServer, err := NewMCPServerWithScriptling(config, logger)
	if err != nil {
		logger.Warn("failed to initialize MCP server", "error", err)
		// Continue running even if MCP server fails - it's optional
	} else {
		router.mcpServer = mcpServer
		logger.Info("initialized MCP server")
	}

	// Initialize shared storage
	convTTL := time.Duration(config.Conversations.TTLDays) * 24 * time.Hour
	if config.Conversations.TTLDays == 0 {
		convTTL = 30 * 24 * time.Hour // Default 30 days
	}
	sharedStore, err := storage.NewStore(config.Storage.Path, convTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	router.sharedStore = sharedStore

	// Initialize responses service
	router.responsesService = responses.NewService(config.Responses.TTLDays)
	logger.Info("initialized responses service")

	// Initialize conversations service
	conversationsService, err := conversations.NewService(sharedStore, &config.Conversations)
	if err != nil {
		logger.Warn("failed to initialize conversations service", "error", err)
	} else {
		router.conversationsService = conversationsService
		logger.Info("initialized conversations service")
	}

	// Setup HTTP mux with auth middleware
	auth := middleware.Auth(config.Server.Token)
	router.mux = http.NewServeMux()
	router.mux.HandleFunc("GET /v1/models", auth(router.HandleModels))
	router.mux.HandleFunc("POST /v1/chat/completions", auth(router.HandleChatCompletions))
	router.mux.HandleFunc("POST /v1/messages", auth(router.HandleMessages))
	router.mux.HandleFunc("POST /v1/messages/count_tokens", auth(router.HandleCountTokens))
	router.mux.HandleFunc("POST /v1/embeddings", auth(router.HandleEmbeddings))
	router.mux.HandleFunc("GET /ollama/v1/models", auth(router.HandleModels))
	router.mux.HandleFunc("POST /ollama/v1/chat/completions", auth(router.HandleChatCompletions))
	router.mux.HandleFunc("POST /ollama/v1/messages", auth(router.HandleMessages))
	router.mux.HandleFunc("POST /ollama/v1/messages/count_tokens", auth(router.HandleCountTokens))
	router.mux.HandleFunc("POST /ollama/v1/embeddings", auth(router.HandleEmbeddings))
	router.mux.HandleFunc("GET /ollama/api/version", auth(router.HandleOllamaVersion))
	router.mux.HandleFunc("GET /ollama/api/tags", auth(router.HandleOllamaTags))
	router.mux.HandleFunc("GET /ollama/api/ps", auth(router.HandleOllamaRunningModels))
	router.mux.HandleFunc("POST /ollama/api/chat", auth(router.HandleOllamaChat))
	router.mux.HandleFunc("POST /ollama/api/generate", auth(router.HandleOllamaGenerate))
	router.mux.HandleFunc("POST /ollama/api/embed", auth(router.HandleOllamaEmbed))
	router.mux.HandleFunc("POST /ollama/api/embeddings", auth(router.HandleOllamaEmbeddings))
	router.mux.HandleFunc("POST /ollama/api/show", auth(router.HandleOllamaShow))
	router.mux.HandleFunc("POST /ollama/api/create", auth(router.HandleUnsupported))
	router.mux.HandleFunc("DELETE /ollama/api/delete", auth(router.HandleUnsupported))
	router.mux.HandleFunc("POST /ollama/api/copy", auth(router.HandleUnsupported))
	router.mux.HandleFunc("POST /ollama/api/pull", auth(router.HandleUnsupported))
	router.mux.HandleFunc("POST /ollama/api/push", auth(router.HandleUnsupported))
	router.mux.HandleFunc("GET /health", router.HandleHealth) // Health endpoint is not protected

	// Add responses endpoints if service is available
	if router.responsesService != nil {
		router.mux.HandleFunc("POST /v1/responses", auth(router.HandleCreateResponse))
		router.mux.HandleFunc("GET /v1/responses/{id}", auth(router.HandleGetResponse))
		router.mux.HandleFunc("DELETE /v1/responses/{id}", auth(router.HandleDeleteResponse))
		router.mux.HandleFunc("GET /v1/responses", auth(router.HandleListResponses))
		router.mux.HandleFunc("POST /v1/responses/{id}/cancel", auth(router.HandleCancelResponse))
		router.mux.HandleFunc("POST /v1/responses/{id}/compact", auth(router.HandleCompactResponses))
		router.mux.HandleFunc("GET /v1/responses/{id}/input-items", auth(router.HandleUnsupported))
		router.mux.HandleFunc("GET /v1/responses/{id}/input-tokens", auth(router.HandleUnsupported))
		router.mux.HandleFunc("POST /ollama/v1/responses", auth(router.HandleCreateResponse))
		router.mux.HandleFunc("GET /ollama/v1/responses/{id}", auth(router.HandleGetResponse))
		router.mux.HandleFunc("DELETE /ollama/v1/responses/{id}", auth(router.HandleDeleteResponse))
		router.mux.HandleFunc("GET /ollama/v1/responses", auth(router.HandleListResponses))
		router.mux.HandleFunc("POST /ollama/v1/responses/{id}/cancel", auth(router.HandleCancelResponse))
		router.mux.HandleFunc("POST /ollama/v1/responses/{id}/compact", auth(router.HandleCompactResponses))
		router.mux.HandleFunc("GET /ollama/v1/responses/{id}/input-items", auth(router.HandleUnsupported))
		router.mux.HandleFunc("GET /ollama/v1/responses/{id}/input-tokens", auth(router.HandleUnsupported))
		logger.Info("responses endpoints available")
	}

	// Add conversations endpoints if service is available
	if router.conversationsService != nil {
		router.mux.HandleFunc("POST /v1/conversations", auth(router.HandleCreateConversation))
		router.mux.HandleFunc("GET /v1/conversations/{id}", auth(router.HandleGetConversation))
		router.mux.HandleFunc("POST /v1/conversations/{id}", auth(router.HandleUpdateConversation))
		router.mux.HandleFunc("DELETE /v1/conversations/{id}", auth(router.HandleDeleteConversation))
		router.mux.HandleFunc("GET /v1/conversations/{conversation_id}/items", auth(router.HandleListItems))
		router.mux.HandleFunc("POST /v1/conversations/{conversation_id}/items", auth(router.HandleCreateItems))
		router.mux.HandleFunc("GET /v1/conversations/{conversation_id}/items/{item_id}", auth(router.HandleGetItem))
		router.mux.HandleFunc("DELETE /v1/conversations/{conversation_id}/items/{item_id}", auth(router.HandleDeleteItem))
		router.mux.HandleFunc("POST /ollama/v1/conversations", auth(router.HandleCreateConversation))
		router.mux.HandleFunc("GET /ollama/v1/conversations/{id}", auth(router.HandleGetConversation))
		router.mux.HandleFunc("POST /ollama/v1/conversations/{id}", auth(router.HandleUpdateConversation))
		router.mux.HandleFunc("DELETE /ollama/v1/conversations/{id}", auth(router.HandleDeleteConversation))
		router.mux.HandleFunc("GET /ollama/v1/conversations/{conversation_id}/items", auth(router.HandleListItems))
		router.mux.HandleFunc("POST /ollama/v1/conversations/{conversation_id}/items", auth(router.HandleCreateItems))
		router.mux.HandleFunc("GET /ollama/v1/conversations/{conversation_id}/items/{item_id}", auth(router.HandleGetItem))
		router.mux.HandleFunc("DELETE /ollama/v1/conversations/{conversation_id}/items/{item_id}", auth(router.HandleDeleteItem))
		logger.Info("conversations endpoints available")
	}

	// Initialize smart routing if enabled
	if config.SmartRouting.Enabled {
		// Build lib dirs: script dir first, then any additional libpath entries
		var libDirs []string
		if config.SmartRouting.Script != "" {
			libDirs = append(libDirs, filepath.Dir(config.SmartRouting.Script))
		}
		for _, dir := range config.SmartRouting.LibPath {
			if dir != "" {
				libDirs = append(libDirs, dir)
			}
		}

		sr, err := newSmartRouter(
			config.SmartRouting.Script,
			config.SmartRouting.DefaultModel,
			config.SmartRouting.Vars,
			libDirs,
			router,
			logger,
		)
		if err != nil {
			logger.Warn("failed to initialize smart router", "error", err)
		} else {
			router.smartRouter = sr
			logger.Info("smart routing enabled", "script", config.SmartRouting.Script, "default_model", config.SmartRouting.DefaultModel)
		}
	} else {
		logger.Info("smart routing disabled")
	}

	// Add MCP endpoints if server is available
	if router.mcpServer != nil {
		router.mux.HandleFunc("POST /mcp", auth(router.HandleMCP))
		logger.Info("MCP server endpoint available at /mcp (use X-MCP-Tool-Mode: discovery header for discovery mode)")
	}

	// Initialize admin UI if password is configured
	var mcpStorage storage.MCPStorage
	var mcpStorageWritable bool
	if sharedStore != nil {
		mcpStorage = sharedStore.NewMCPStorage()
		mcpStorageWritable = !sharedStore.IsMemory()
	}
	router.mcpStorage = mcpStorage

	router.admin = admin.New(config, router.getStats, router.getProviders, router.getMCPServers, router.getMCPTools, router.getModels, mcpStorage, mcpStorageWritable, router.reloadMCPServers, router.reloadMCPServers)
	if router.admin.Enabled() {
		router.admin.RegisterRoutes(router.mux)
		logger.Info("admin UI enabled at /admin")
	}

	// Start MCP tool cache refresh timer if configured
	if config.MCP.ToolCacheRefreshMinutes > 0 {
		go router.startMCPCacheRefreshTimer(time.Duration(config.MCP.ToolCacheRefreshMinutes) * time.Minute)
		logger.Info("MCP tool cache auto-refresh enabled", "interval_minutes", config.MCP.ToolCacheRefreshMinutes)
	}

	// Add catch-all handler for unmatched routes (must be last)
	router.mux.HandleFunc("/", router.HandleCatchAll)

	return router, nil
}

func (r *Router) RefreshModels(ctx context.Context) error {
	r.logger.Debug("refreshing models from all providers concurrently")

	var wg sync.WaitGroup
	for providerName, provider := range r.Providers {
		if !provider.Enabled {
			continue
		}
		if !provider.Healthy.Load() {
			r.logger.Debug("skipping provider", "provider", providerName, "enabled", provider.Enabled, "healthy", provider.Healthy.Load())
			continue
		}
		if !provider.Fetching.CompareAndSwap(false, true) {
			r.logger.Debug("skipping provider fetch already in progress", "provider", providerName)
			continue
		}
		wg.Add(1)
		go func(name string, p *Provider) {
			defer wg.Done()
			defer p.Fetching.Store(false)
			r.logger.Debug("fetching models from provider", "provider", name)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			modelsResp, err := p.Client.GetModels(ctx)
			if err != nil {
				r.logger.WithError(err).Error("failed to fetch models from provider", "provider", name)
				r.DisableProvider(name, fmt.Sprintf("model fetch failed: %v", err))
				return
			}
			var modelIDs []string
			if len(p.Models) > 0 {
				modelIDs = p.Models
				r.logger.Info("using static models from config", "provider", name, "count", len(modelIDs))
			} else {
				modelIDs = make([]string, 0, len(modelsResp.Data))
				for _, model := range modelsResp.Data {
					modelIDs = append(modelIDs, model.ID)
				}
				r.logger.Debug("fetched models from provider", "provider", name, "count", len(modelsResp.Data), "models", modelIDs)
			}
			r.addProviderModels(name, modelIDs, p)
		}(providerName, provider)
	}
	wg.Wait()

	return nil
}

func (r *Router) addProviderModels(providerName string, modelIDs []string, p *Provider) {
	r.ModelMapMu.Lock()
	defer r.ModelMapMu.Unlock()

	// Remove this provider from all existing model entries
	for modelID, providers := range r.ModelMap {
		newProviders := make([]string, 0, len(providers))
		for _, pn := range providers {
			if pn != providerName {
				newProviders = append(newProviders, pn)
			}
		}
		if len(newProviders) == 0 {
			delete(r.ModelMap, modelID)
		} else {
			r.ModelMap[modelID] = newProviders
		}
	}

	for _, modelID := range modelIDs {
		if !shouldIncludeModel(modelID) {
			continue
		}
		if len(p.ModelAllowlist) > 0 && !inSlice(modelID, p.ModelAllowlist) {
			continue
		}
		if len(p.ModelDenylist) > 0 && inSlice(modelID, p.ModelDenylist) {
			continue
		}
		r.ModelMap[modelID] = append(r.ModelMap[modelID], providerName)
	}

	// Register aliases in ModelMap so they appear in /v1/models and participate
	// in load balancing. The alias→real translation happens per-provider at dispatch.
	for alias := range p.ModelAliases {
		if alias == "" {
			continue
		}
		if !inSlice(providerName, r.ModelMap[alias]) {
			r.ModelMap[alias] = append(r.ModelMap[alias], providerName)
		}
	}

	for modelID, tags := range p.ModelTags {
		for _, t := range tags {
			if !inSlice(t, r.ModelTags[modelID]) {
				r.ModelTags[modelID] = append(r.ModelTags[modelID], t)
			}
		}
	}

	r.logger.Info("model refresh complete", "total_models", len(r.ModelMap), "total_providers", len(r.Providers))
}

// DisableProvider marks a provider as unhealthy and removes its models from the map
func (r *Router) DisableProvider(providerName, reason string) {
	r.ModelMapMu.Lock()
	defer r.ModelMapMu.Unlock()

	provider, exists := r.Providers[providerName]
	if !exists {
		return
	}

	if !provider.Healthy.Load() {
		return // Already disabled
	}

	provider.Healthy.Store(false)

	r.logger.Warn("provider disabled", "provider", providerName, "reason", reason)

	// Remove all models from this provider
	modelsToRemove := make([]string, 0)
	for modelID, providers := range r.ModelMap {
		newProviders := make([]string, 0, len(providers))
		for _, p := range providers {
			if p != providerName {
				newProviders = append(newProviders, p)
			}
		}
		if len(newProviders) == 0 {
			modelsToRemove = append(modelsToRemove, modelID)
		} else {
			r.ModelMap[modelID] = newProviders
		}
	}

	// Remove models that have no providers left
	for _, modelID := range modelsToRemove {
		delete(r.ModelMap, modelID)
	}

	r.logger.Info("removed models from disabled provider",
		"provider", providerName,
		"models_removed", len(modelsToRemove))
}

// EnableProvider marks a provider as healthy again
func (r *Router) EnableProvider(providerName string) {
	provider, exists := r.Providers[providerName]
	if !exists {
		return
	}

	if provider.Healthy.Load() {
		return // Already enabled
	}

	provider.Healthy.Store(true)
	r.logger.Info("provider re-enabled", "provider", providerName)
}

// shouldIncludeModel checks if a model should be included (no filtering in new spec)
func shouldIncludeModel(model string) bool {
	return model != ""
}

func inSlice(s string, slice []string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// resolveAliasForProvider returns the real model name for a given provider,
// or the alias unchanged if the provider has no mapping for it.
func (r *Router) resolveAliasForProvider(model, providerName string) string {
	if p, ok := r.Providers[providerName]; ok {
		if real, ok := p.ModelAliases[model]; ok && real != "" {
			return real
		}
	}
	return model
}

func (r *Router) GetProviderForModel(model string, hint string) (string, error) {
	r.ModelMapMu.RLock()
	providers, exists := r.ModelMap[model]
	r.ModelMapMu.RUnlock()

	if !exists {
		return "", fmt.Errorf("model %s not found in any provider", model)
	}

	if len(providers) == 1 {
		return providers[0], nil
	}

	// Select provider with best score: lowest load adjusted by weight
	// score = ActiveCompletions / weight (lower is better)
	// Ties are broken randomly to ensure distribution under concurrent load.
	var tiedProviders []string
	bestScore := float64(-1)

	for _, providerName := range providers {
		provider, exists := r.Providers[providerName]
		if !exists || !provider.Enabled || !provider.Healthy.Load() {
			continue
		}
		w := provider.Weight
		if w <= 0 {
			w = 1.0
		}
		score := float64(provider.ActiveCompletions.Load()) / w
		if bestScore < 0 || score < bestScore {
			bestScore = score
			tiedProviders = []string{providerName}
		} else if score == bestScore {
			tiedProviders = append(tiedProviders, providerName)
		}
	}

	if len(tiedProviders) == 0 {
		return "", fmt.Errorf("no enabled provider found for model %s", model)
	}

	selectedProvider := tiedProviders[0]
	if len(tiedProviders) > 1 {
		selectedProvider = tiedProviders[rand.Intn(len(tiedProviders))]
	}

	// Honour the hint if the hinted provider is healthy and its score is within
	// bestScore + 1.0 (one extra active completion adjusted for weight).
	if hint != "" && hint != selectedProvider {
		if p, ok := r.Providers[hint]; ok && p.Enabled && p.Healthy.Load() {
			w := p.Weight
			if w <= 0 {
				w = 1.0
			}
			hintScore := float64(p.ActiveCompletions.Load()) / w
			if hintScore <= bestScore+1.0 {
				selectedProvider = hint
			}
		}
	}

	return selectedProvider, nil
}

func (r *Router) ListModels() ModelsResponse {
	r.ModelMapMu.RLock()
	defer r.ModelMapMu.RUnlock()

	models := make([]Model, 0, len(r.ModelMap)+1)
	for modelID := range r.ModelMap {
		models = append(models, Model{
			ID:      modelID,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "router",
		})
	}

	// Inject "auto" model if smart routing is enabled
	if r.smartRouter != nil {
		models = append(models, Model{
			ID:      "auto",
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "router",
		})
	}

	// Sort models by ID for consistent ordering
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	return ModelsResponse{
		Object: "list",
		Data:   models,
	}
}

type anthropicModelsResponse = claude.ModelsListResponse

func (r *Router) ListModelsAnthropic() anthropicModelsResponse {
	r.ModelMapMu.RLock()
	defer r.ModelMapMu.RUnlock()

	models := make([]claude.ModelInfo, 0, len(r.ModelMap))
	for modelID := range r.ModelMap {
		models = append(models, claude.ModelInfo{
			Type:        "model",
			ID:          modelID,
			DisplayName: modelID,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}

	if r.smartRouter != nil {
		models = append(models, claude.ModelInfo{
			Type:        "model",
			ID:          "auto",
			DisplayName: "auto",
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	resp := anthropicModelsResponse{Data: models, HasMore: false}
	if len(models) > 0 {
		resp.FirstID = models[0].ID
		resp.LastID = models[len(models)-1].ID
	}
	return resp
}

func (r *Router) CreateChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	providerHint := ""
	if req.Model == "auto" && r.smartRouter != nil {
		result := r.smartRouter.Route(ctx, req)
		if result.Model == "" {
			return nil, fmt.Errorf("model auto not found in any provider")
		}
		req.Model = result.Model
		providerHint = result.ProviderHint
	}

	providerName, err := r.GetProviderForModel(req.Model, providerHint)
	if err != nil {
		return nil, err
	}

	provider := r.Providers[providerName]
	r.incrementActiveCompletions(providerName)
	defer r.decrementActiveCompletions(providerName)

	// Resolve alias to the real model name for this specific provider
	dispatchReq := *req
	dispatchReq.Model = r.resolveAliasForProvider(req.Model, providerName)
	r.logger.Debug("routing chat completion", "alias", req.Model, "model", dispatchReq.Model, "provider", providerName)
	r.traceRequestPayload("chat completion", &dispatchReq)

	resp, err := provider.Client.ChatCompletion(ctx, dispatchReq)
	if err != nil {
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}

	return resp, nil
}

func (r *Router) CreateEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	providerName, err := r.GetProviderForModel(req.Model, "")
	if err != nil {
		return nil, err
	}

	provider := r.Providers[providerName]
	r.logger.Info("routing embedding request", "model", req.Model, "provider", providerName)

	dispatchReq := *req
	dispatchReq.Model = r.resolveAliasForProvider(req.Model, providerName)
	resp, err := provider.Client.CreateEmbedding(ctx, dispatchReq)
	if err != nil {
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}

	return resp, nil
}

func (r *Router) streamChatCompletion(ctx context.Context, providerName string, req *ChatCompletionRequest) *openai.ChatStream {
	provider := r.Providers[providerName]
	r.incrementActiveCompletions(providerName)
	dispatchReq := *req
	dispatchReq.Model = r.resolveAliasForProvider(req.Model, providerName)
	r.traceRequestPayload("streaming chat completion", &dispatchReq)
	return provider.Client.StreamChatCompletion(ctx, dispatchReq)
}

func (r *Router) writeStream(w http.ResponseWriter, stream *openai.ChatStream, model, providerName string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	for stream.Next() {
		data, err := json.Marshal(stream.Current())
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	if err := stream.Err(); err != nil {
		r.logger.WithError(err).Error("streaming error", "model", model, "provider", providerName)
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	r.logger.Debug("streaming response completed", "model", model, "provider", providerName)
}

func (r *Router) traceRequestPayload(label string, req *ChatCompletionRequest) {
	if !r.traceEnabled {
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		r.logger.Trace("failed to marshal request payload for logging", "label", label, "error", err)
		return
	}
	r.logger.Trace(label+" request payload", "payload", string(payload))
}

func (r *Router) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Common connection error patterns
	connectionPatterns := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
		"no such host",
		"network is unreachable",
		"temporary failure",
		"dial tcp",
		"eof",
		"connection closed",
	}

	for _, pattern := range connectionPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	// Also detect fatal API errors that indicate a broken provider/model
	fatalAPIPatterns := []string{
		"missing tensor",                        // Corrupted GGUF file (Ollama)
		"llama runner process has terminated",   // Model loading failure (Ollama)
		"model runner has unexpectedly stopped", // Ollama model runtime failure
	}

	for _, pattern := range fatalAPIPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

func (r *Router) incrementActiveCompletions(providerName string) {
	if provider, exists := r.Providers[providerName]; exists {
		provider.ActiveCompletions.Add(1)
	}
}

func (r *Router) decrementActiveCompletions(providerName string) {
	if provider, exists := r.Providers[providerName]; exists {
		provider.ActiveCompletions.Add(-1)
	}
}

// HTTP Handlers
func (r *Router) HandleModels(w http.ResponseWriter, req *http.Request) {
	r.RefreshModels(req.Context())

	w.Header().Set("Content-Type", "application/json")
	if req.Header.Get("anthropic-version") != "" {
		if err := writeJSON(w, r.ListModelsAnthropic()); err != nil {
			r.logger.WithError(err).Error("failed to write models response")
		}
	} else {
		if err := writeJSON(w, r.ListModels()); err != nil {
			r.logger.WithError(err).Error("failed to write models response")
		}
	}
}

func (r *Router) HandleChatCompletions(w http.ResponseWriter, req *http.Request) {
	var completionReq ChatCompletionRequest
	if err := readJSON(req, &completionReq); err != nil {
		r.logger.WithError(err).Error("failed to parse chat completion request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Check if client requested streaming
	if completionReq.Stream {
		r.handleStreamingChatCompletion(w, req, &completionReq)
	} else {
		r.handleNonStreamingChatCompletion(w, req, &completionReq)
	}
}

func (r *Router) handleNonStreamingChatCompletion(w http.ResponseWriter, req *http.Request, completionReq *ChatCompletionRequest) {
	ctx := req.Context()

	resp, err := r.CreateChatCompletion(ctx, completionReq)
	if err != nil {
		r.logger.WithError(err).Error("chat completion failed")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write chat completion response")
	}
}

func (r *Router) handleStreamingChatCompletion(w http.ResponseWriter, req *http.Request, completionReq *ChatCompletionRequest) {
	ctx := req.Context()

	// Resolve "auto" model before streaming
	if completionReq.Model == "auto" && r.smartRouter != nil {
		result := r.smartRouter.Route(ctx, completionReq)
		if result.Model == "" {
			http.Error(w, "auto routing failed: no model available", http.StatusServiceUnavailable)
			return
		}
		completionReq.Model = result.Model
		// stash hint for GetProviderForModel below
		providerName, err := r.GetProviderForModel(completionReq.Model, result.ProviderHint)
		if err != nil {
			r.logger.WithError(err).Error("streaming chat completion failed")
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
			} else {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}
		stream := r.streamChatCompletion(ctx, providerName, completionReq)
		defer r.decrementActiveCompletions(providerName)
		r.writeStream(w, stream, completionReq.Model, providerName)
		return
	}

	providerName, err := r.GetProviderForModel(completionReq.Model, "")
	if err != nil {
		r.logger.WithError(err).Error("streaming chat completion failed")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	stream := r.streamChatCompletion(ctx, providerName, completionReq)
	defer r.decrementActiveCompletions(providerName)
	r.writeStream(w, stream, completionReq.Model, providerName)
}

func (r *Router) HandleMessages(w http.ResponseWriter, req *http.Request) {
	var messagesReq claude.MessagesRequest
	if err := readJSON(req, &messagesReq); err != nil {
		r.logger.WithError(err).Error("failed to parse messages request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	openaiReq := claude.MessagesRequestToOpenAI(&messagesReq)
	ctx := req.Context()

	if messagesReq.Stream {
		providerHint := ""
		if openaiReq.Model == "auto" && r.smartRouter != nil {
			result := r.smartRouter.Route(ctx, &openaiReq)
			if result.Model == "" {
				http.Error(w, "auto routing failed: no model available", http.StatusServiceUnavailable)
				return
			}
			openaiReq.Model = result.Model
			providerHint = result.ProviderHint
		}
		providerName, err := r.GetProviderForModel(openaiReq.Model, providerHint)
		if err != nil {
			r.logger.WithError(err).Error("messages stream request failed")
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		stream := r.streamChatCompletion(ctx, providerName, &openaiReq)
		defer r.decrementActiveCompletions(providerName)
		flush := func() {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err := claude.StreamOpenAIToMessages(w, flush, stream, openaiReq.Model); err != nil {
			r.logger.WithError(err).Error("messages stream error")
		}
		return
	}

	resp, err := r.createChatCompletionWithHeaders(ctx, &openaiReq, passthroughHeaders(req))
	if err != nil {
		r.logger.WithError(err).Error("messages request failed")
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, claude.OpenAIToMessagesResponse(resp)); err != nil {
		r.logger.WithError(err).Error("failed to write messages response")
	}
}

// passthroughHeaders returns headers from the incoming request, excluding auth and hop-by-hop headers.
var skipHeaders = map[string]bool{
	"authorization":     true,
	"x-api-key":         true,
	"content-length":    true,
	"content-type":      true,
	"host":              true,
	"connection":        true,
	"transfer-encoding": true,
}

func passthroughHeaders(req *http.Request) http.Header {
	h := make(http.Header)
	for k, v := range req.Header {
		if !skipHeaders[strings.ToLower(k)] {
			h[k] = v
		}
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// createChatCompletionWithHeaders routes a chat completion, using a temporary client
// with extra headers for claude providers.
func (r *Router) createChatCompletionWithHeaders(ctx context.Context, req *ChatCompletionRequest, extraHeaders http.Header) (*ChatCompletionResponse, error) {
	if len(extraHeaders) == 0 {
		return r.CreateChatCompletion(ctx, req)
	}

	providerHint := ""
	if req.Model == "auto" && r.smartRouter != nil {
		result := r.smartRouter.Route(ctx, req)
		if result.Model == "" {
			return nil, fmt.Errorf("model auto not found in any provider")
		}
		req.Model = result.Model
		providerHint = result.ProviderHint
	}

	providerName, err := r.GetProviderForModel(req.Model, providerHint)
	if err != nil {
		return nil, err
	}

	provider := r.Providers[providerName]
	if provider.ProviderType != "claude" {
		return r.CreateChatCompletion(ctx, req)
	}

	// Find provider config to create a temporary client with extra headers
	var providerCfg *types.ProviderConfig
	for i := range r.config.Providers {
		if r.config.Providers[i].Name == providerName {
			providerCfg = &r.config.Providers[i]
			break
		}
	}
	if providerCfg == nil {
		return r.CreateChatCompletion(ctx, req)
	}

	client, err := newAIClientWithHeaders(providerCfg, extraHeaders)
	if err != nil {
		return r.CreateChatCompletion(ctx, req)
	}

	r.incrementActiveCompletions(providerName)
	defer r.decrementActiveCompletions(providerName)

	dispatchReq := *req
	dispatchReq.Model = r.resolveAliasForProvider(req.Model, providerName)
	r.logger.Debug("routing chat completion with passthrough headers", "alias", req.Model, "model", dispatchReq.Model, "provider", providerName)
	r.traceRequestPayload("chat completion (passthrough)", &dispatchReq)

	resp, err := client.ChatCompletion(ctx, dispatchReq)
	if err != nil {
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}
	return resp, nil
}
func (r *Router) HandleEmbeddings(w http.ResponseWriter, req *http.Request) {
	var embeddingReq EmbeddingRequest
	if err := readJSON(req, &embeddingReq); err != nil {
		r.logger.WithError(err).Error("failed to parse embedding request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ctx := req.Context()
	resp, err := r.CreateEmbedding(ctx, &embeddingReq)
	if err != nil {
		r.logger.WithError(err).Error("embedding request failed")

		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write embedding response")
	}
}

func (r *Router) HandleHealth(w http.ResponseWriter, req *http.Request) {
	r.ModelMapMu.RLock()
	defer r.ModelMapMu.RUnlock()

	health := map[string]interface{}{
		"status":    "ok",
		"providers": len(r.Providers),
		"models":    len(r.ModelMap),
	}

	// Add provider status
	providerStatus := make(map[string]interface{})
	for name, provider := range r.Providers {
		providerStatus[name] = map[string]interface{}{
			"enabled":            provider.Enabled,
			"healthy":            provider.Healthy.Load(),
			"active_completions": provider.ActiveCompletions.Load(),
		}
	}
	health["provider_status"] = providerStatus

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, health); err != nil {
		r.logger.WithError(err).Error("failed to write health response")
	}
}

// Helper functions for JSON handling
func readJSON(req *http.Request, v interface{}) error {
	defer req.Body.Close()
	return json.NewDecoder(req.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}

// HandleMCP handles MCP protocol requests.
// The tool mode is determined from the X-MCP-Tool-Mode header or tool_mode query parameter.
// Use X-MCP-Tool-Mode: discovery header to enable discovery mode.
func (r *Router) HandleMCP(w http.ResponseWriter, req *http.Request) {
	if r.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusServiceUnavailable)
		return
	}

	r.mcpServer.HandleRequest(w, req)
}

// InitMCPServers loads MCP servers from storage; call after the HTTP server is listening.
func (r *Router) InitMCPServers() {
	r.reloadMCPServers()
}

// StartBackgroundTasks starts the background health check task
func (r *Router) StartBackgroundTasks() {
	r.wg.Add(1)
	go r.healthCheckTask()
}

// StopBackgroundTasks stops all background tasks
func (r *Router) StopBackgroundTasks() {
	r.shutdownOnce.Do(func() {
		close(r.shutdownChan)
	})
	r.wg.Wait()
}

// healthCheckTask runs every 30 seconds to check disabled providers
func (r *Router) healthCheckTask() {
	defer r.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.shutdownChan:
			r.logger.Info("health check task stopping")
			return
		case <-ticker.C:
			r.checkDisabledProviders()
		}
	}
}

// checkDisabledProviders attempts to reconnect disabled providers
func (r *Router) checkDisabledProviders() {
	unhealthyProviders := make([]string, 0)

	// Find all unhealthy enabled providers
	for name, provider := range r.Providers {
		if provider.Enabled && !provider.Healthy.Load() {
			unhealthyProviders = append(unhealthyProviders, name)
		}
	}

	if len(unhealthyProviders) == 0 {
		return
	}

	r.logger.Debug("checking disabled providers", "count", len(unhealthyProviders))

	// Check each unhealthy provider concurrently
	var wg sync.WaitGroup
	for _, providerName := range unhealthyProviders {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()

			r.logger.Debug("checking provider health", "provider", name)

			// Try to fetch models with a short timeout
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			provider := r.Providers[name]
			_, err := provider.Client.GetModels(ctx)
			if err != nil {
				r.logger.Debug("provider still unhealthy", "provider", name, "error", err)
				return
			}

			// Provider is healthy again, re-enable and fetch its models
			r.EnableProvider(name)
			r.logger.Info("provider recovered and re-enabled", "provider", name)
			provider.Fetching.Store(false) // reset so RefreshModels can pick it up
			go r.RefreshModels(context.Background())
		}(providerName)
	}

	wg.Wait()
}

// ServeHTTP implements http.Handler
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.logger.Trace("request received",
		"method", req.Method,
		"path", req.URL.Path,
		"query", req.URL.RawQuery,
		"remote_addr", req.RemoteAddr,
		"user_agent", req.Header.Get("User-Agent"),
	)

	rec := &statusLoggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
	r.mux.ServeHTTP(rec, req)

	r.logger.Trace("request completed",
		"method", req.Method,
		"path", req.URL.Path,
		"query", req.URL.RawQuery,
		"status", rec.statusCode,
		"remote_addr", req.RemoteAddr,
		"user_agent", req.Header.Get("User-Agent"),
	)
}

// getStats returns statistics for the admin UI
func (r *Router) getStats() *admin.Stats {
	r.ModelMapMu.RLock()
	modelCount := len(r.ModelMap)
	r.ModelMapMu.RUnlock()

	mcpServerCount := len(r.config.MCP.RemoteServers)
	if r.mcpStorage != nil {
		storageServers, err := r.mcpStorage.List(context.Background())
		if err == nil {
			mcpServerCount += len(storageServers)
		}
	}

	// Sum active completions from all providers
	activeRequests := 0
	for _, p := range r.Providers {
		activeRequests += int(p.ActiveCompletions.Load())
	}

	return &admin.Stats{
		Providers:      len(r.Providers),
		Models:         modelCount,
		MCPServers:     mcpServerCount,
		ActiveRequests: activeRequests,
	}
}

// getProviders returns provider information for the admin UI
func (r *Router) getProviders() []admin.ProviderInfo {
	providers := make([]admin.ProviderInfo, 0, len(r.Providers))
	for name, p := range r.Providers {
		modelCount := 0
		r.ModelMapMu.RLock()
		for _, providerNames := range r.ModelMap {
			for _, pn := range providerNames {
				if pn == name {
					modelCount++
				}
			}
		}
		r.ModelMapMu.RUnlock()

		providers = append(providers, admin.ProviderInfo{
			Name:       name,
			Type:       p.ProviderType,
			Healthy:    p.Healthy.Load(),
			ModelCount: modelCount,
			Weight:     p.Weight,
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers
}

// getMCPServers returns MCP server information for the admin UI
func (r *Router) getMCPServers() []admin.MCPServerInfo {
	servers := make([]admin.MCPServerInfo, 0, len(r.config.MCP.RemoteServers))

	// Add static servers from config
	for _, s := range r.config.MCP.RemoteServers {
		servers = append(servers, admin.MCPServerInfo{
			Namespace:      s.Namespace,
			URL:            s.URL,
			Enabled:        true,
			ToolVisibility: s.ToolVisibility,
			ToolAllowlist:  s.ToolAllowlist,
			ToolDenylist:   s.ToolDenylist,
			StaticServer:   true,
			RemoteSearch:   s.RemoteSearch,
		})
	}

	// Add dynamic servers from storage
	if r.mcpStorage != nil {
		storageServers, err := r.mcpStorage.List(context.Background())
		if err == nil {
			for _, s := range storageServers {
				servers = append(servers, admin.MCPServerInfo{
					Namespace:      s.Namespace,
					URL:            s.URL,
					AuthType:       s.AuthType,
					Enabled:        s.Enabled,
					ToolVisibility: s.ToolVisibility,
					ToolAllowlist:  s.ToolAllowlist,
					ToolDenylist:   s.ToolDenylist,
					StaticServer:   false,
					RemoteSearch:   s.RemoteSearch,
				})
			}
		}
	}

	sort.Slice(servers, func(i, j int) bool { return servers[i].Namespace < servers[j].Namespace })
	return servers
}

// getMCPTools returns tools for an MCP server
func (r *Router) getMCPTools(namespace string) ([]admin.ToolInfo, error) {
	if r.mcpServer == nil {
		return nil, fmt.Errorf("MCP server not available")
	}

	// First check if this is a storage-based server
	if r.mcpStorage != nil {
		server, err := r.mcpStorage.Get(context.Background(), namespace)
		if err == nil && server != nil {
			// It's a storage-based server, get tools with disabled state
			return r.mcpServer.GetStorageServerTools(namespace, server)
		}
	}

	// Fall back to config-based server
	return r.mcpServer.GetToolsForAdmin(namespace)
}

// reloadMCPServers reloads MCP servers from storage
// This is called when servers are added/updated/deleted via the admin UI
func (r *Router) reloadMCPServers() {
	if r.mcpServer == nil {
		return
	}

	r.logger.Info("reloading MCP servers")

	// Get all storage-based servers
	var servers []*storage.MCPServerConfig
	if r.mcpStorage != nil {
		var err error
		servers, err = r.mcpStorage.List(context.Background())
		if err != nil {
			r.logger.Warn("failed to list MCP servers from storage", "error", err)
			return
		}
	}

	// Atomically reload all servers (static + storage-based)
	r.mcpServer.ReloadAllServers(servers)
}

// startMCPCacheRefreshTimer periodically refreshes the MCP tool cache
func (r *Router) startMCPCacheRefreshTimer(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.shutdownChan:
			return
		case <-ticker.C:
			r.logger.Info("auto-refreshing MCP tool cache")
			r.reloadMCPServers()
		}
	}
}

// getModels returns model information for the admin UI
func (r *Router) getModels() []admin.ModelInfo {
	r.ModelMapMu.RLock()
	defer r.ModelMapMu.RUnlock()

	models := make([]admin.ModelInfo, 0, len(r.ModelMap))
	for modelID, providers := range r.ModelMap {
		providersCopy := make([]string, len(providers))
		copy(providersCopy, providers)
		sort.Strings(providersCopy)
		models = append(models, admin.ModelInfo{
			ID:        modelID,
			Providers: providersCopy,
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

// Shutdown gracefully shuts down the router
func (r *Router) Shutdown() {
	r.shutdownOnce.Do(func() {
		close(r.shutdownChan)
		if r.smartRouter != nil {
			r.smartRouter.Stop()
		}
		if r.sharedStore != nil {
			r.sharedStore.Close()
		}
	})
	r.wg.Wait()
}

// Responses HTTP Handlers
func (r *Router) HandleCreateResponse(w http.ResponseWriter, req *http.Request) {
	r.logger.Trace("HandleCreateResponse")

	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	var createReq CreateResponseRequest
	if err := readJSON(req, &createReq); err != nil {
		r.logger.WithError(err).Error("failed to parse create response request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	providerName, err := r.GetProviderForModel(createReq.Model, "")
	if err != nil {
		r.logger.WithError(err).Error("no provider for model")
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	createReq.Model = r.resolveAliasForProvider(createReq.Model, providerName)
	resp, err := r.responsesService.CreateResponse(req.Context(), r.Providers[providerName].Client, &createReq)
	if err != nil {
		r.logger.WithError(err).Error("failed to create response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleGetResponse(w http.ResponseWriter, req *http.Request) {
	r.logger.Trace("HandleGetResponse")

	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Response ID required", http.StatusBadRequest)
		return
	}

	resp, err := r.responsesService.GetResponse(req.Context(), id)
	if err != nil {
		if err.Error() == "response not found" {
			http.Error(w, "Response not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to get response")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleDeleteResponse(w http.ResponseWriter, req *http.Request) {
	r.logger.Trace("HandleDeleteResponse")

	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Response ID required", http.StatusBadRequest)
		return
	}

	if err := r.responsesService.DeleteResponse(req.Context(), id); err != nil {
		if err.Error() == "response not found" {
			http.Error(w, "Response not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to delete response")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) HandleListResponses(w http.ResponseWriter, req *http.Request) {
	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	resp, err := r.responsesService.ListResponses(req.Context())
	if err != nil {
		r.logger.WithError(err).Error("failed to list responses")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleCancelResponse(w http.ResponseWriter, req *http.Request) {
	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Response ID required", http.StatusBadRequest)
		return
	}

	resp, err := r.responsesService.CancelResponse(req.Context(), id)
	if err != nil {
		if err.Error() == "response not found" {
			http.Error(w, "Response not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to cancel response")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleCompactResponses(w http.ResponseWriter, req *http.Request) {
	if r.responsesService == nil {
		http.Error(w, "Responses service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Response ID required", http.StatusBadRequest)
		return
	}

	resp, err := r.responsesService.CompactResponse(req.Context(), id)
	if err != nil {
		if err.Error() == "response not found" {
			http.Error(w, "Response not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to compact response")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleCountTokens(w http.ResponseWriter, req *http.Request) {
	var messagesReq claude.MessagesRequest
	if err := readJSON(req, &messagesReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	openaiReq := claude.MessagesRequestToOpenAI(&messagesReq)
	tc := openai.NewTokenCounter()
	tc.AddPromptTokensFromMessages(openaiReq.Messages)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]int{"input_tokens": tc.GetUsage().PromptTokens})
}

func (r *Router) HandleUnsupported(w http.ResponseWriter, req *http.Request) {
	http.Error(w, "Not supported", http.StatusNotFound)
}

// HandleCatchAll handles all unmatched routes and logs a warning
func (r *Router) HandleCatchAll(w http.ResponseWriter, req *http.Request) {
	// Redirect root to admin
	if req.URL.Path == "/" {
		http.Redirect(w, req, "/admin", http.StatusFound)
		return
	}

	// Handle CORS preflight silently
	if req.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, anthropic-version, x-api-key")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	r.logger.Warn("unhandled request", "method", req.Method, "path", req.URL.Path, "query", req.URL.RawQuery, "user_agent", req.Header.Get("User-Agent"))

	// Serve styled 404 page if admin is enabled
	if r.admin != nil && r.admin.Enabled() {
		r.admin.Serve404(w, req)
		return
	}

	http.NotFound(w, req)
}

// Helper function to parse integer parameters
func parseIntParam(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}
	var result int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		result = result*10 + int(c-'0')
	}
	return result, nil
}

// Conversation HTTP Handlers
func (r *Router) HandleCreateConversation(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	var createReq openai.CreateConversationRequest
	if err := readJSON(req, &createReq); err != nil {
		r.logger.WithError(err).Error("failed to parse create conversation request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	conversation, err := r.conversationsService.CreateConversation(req.Context(), &createReq)
	if err != nil {
		r.logger.WithError(err).Error("failed to create conversation")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := writeJSON(w, conversation); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleGetConversation(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	conversation, err := r.conversationsService.GetConversation(req.Context(), id)
	if err != nil {
		if err.Error() == "conversation not found" {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to get conversation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, conversation); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleUpdateConversation(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	var updateReq openai.UpdateConversationRequest
	if err := readJSON(req, &updateReq); err != nil {
		r.logger.WithError(err).Error("failed to parse update conversation request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	conversation, err := r.conversationsService.UpdateConversation(req.Context(), id, &updateReq)
	if err != nil {
		if err.Error() == "conversation not found" {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to update conversation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, conversation); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleDeleteConversation(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	id := req.PathValue("id")
	if id == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	deleteResp, err := r.conversationsService.DeleteConversation(req.Context(), id)
	if err != nil {
		if err.Error() == "conversation not found" {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to delete conversation")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, deleteResp); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleListItems(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	conversationID := req.PathValue("conversation_id")
	if conversationID == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	// Parse query parameters
	after := req.URL.Query().Get("after")
	limit := 20 // default
	if limitStr := req.URL.Query().Get("limit"); limitStr != "" {
		if l, err := parseIntParam(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	order := req.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}
	include := req.URL.Query()["include"]

	items, err := r.conversationsService.ListItems(req.Context(), conversationID, after, limit, order, include)
	if err != nil {
		if err.Error() == "conversation not found" {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to list items")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, items); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleCreateItems(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	conversationID := req.PathValue("conversation_id")
	if conversationID == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	var createReq openai.CreateItemsRequest
	if err := readJSON(req, &createReq); err != nil {
		r.logger.WithError(err).Error("failed to parse create items request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	include := req.URL.Query()["include"]

	items, err := r.conversationsService.CreateItems(req.Context(), conversationID, &createReq, include)
	if err != nil {
		if err.Error() == "conversation not found" {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to create items")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := writeJSON(w, items); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleGetItem(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	conversationID := req.PathValue("conversation_id")
	itemID := req.PathValue("item_id")
	if conversationID == "" || itemID == "" {
		http.Error(w, "Conversation ID and Item ID required", http.StatusBadRequest)
		return
	}

	include := req.URL.Query()["include"]

	item, err := r.conversationsService.GetItem(req.Context(), conversationID, itemID, include)
	if err != nil {
		if err.Error() == "conversation not found" || err.Error() == "item not found" {
			http.Error(w, "Not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to get item")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, item); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleDeleteItem(w http.ResponseWriter, req *http.Request) {
	if r.conversationsService == nil {
		http.Error(w, "Conversations service not available", http.StatusServiceUnavailable)
		return
	}

	conversationID := req.PathValue("conversation_id")
	itemID := req.PathValue("item_id")
	if conversationID == "" || itemID == "" {
		http.Error(w, "Conversation ID and Item ID required", http.StatusBadRequest)
		return
	}

	conversation, err := r.conversationsService.DeleteItem(req.Context(), conversationID, itemID)
	if err != nil {
		if err.Error() == "conversation not found" || err.Error() == "item not found" {
			http.Error(w, "Not found", http.StatusNotFound)
		} else {
			r.logger.WithError(err).Error("failed to delete item")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, conversation); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}
