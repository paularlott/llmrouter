package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/conversations"
	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/middleware"
	"github.com/paularlott/mcp/openai"
)

func NewRouter(config *Config, logger Logger) (*Router, error) {
	router := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		config:       config,
		logger:       logger,
		shutdownChan: make(chan struct{}),
	}

	// Initialize providers
	for _, providerConfig := range config.Providers {
		if !providerConfig.Enabled {
			continue
		}

		provider := &Provider{
			Name:              providerConfig.Name,
			BaseURL:           providerConfig.BaseURL,
			Token:             providerConfig.Token,
			Enabled:           providerConfig.Enabled,
			Healthy:           true, // Start as healthy, will be verified
			Client:            NewOpenAIClient(providerConfig.BaseURL, providerConfig.Token, logger),
			ActiveCompletions: 0,
			StaticModels:      len(providerConfig.Models) > 0, // Static if models are provided in config
			Allowlist:         providerConfig.Allowlist,
			Denylist:          providerConfig.Denylist,
			NativeResponses:   providerConfig.NativeResponses,
		}

		router.Providers[provider.Name] = provider
		logger.Info("initialized provider", "name", provider.Name, "base_url", provider.BaseURL)
	}

	// Initialize MCP server
	mcpServer, err := NewMCPServer(config, logger, router)
	if err != nil {
		logger.Warn("failed to initialize MCP server", "error", err)
		// Continue running even if MCP server fails - it's optional
	} else {
		router.mcpServer = mcpServer
		logger.Info("initialized MCP server")
	}

	// Initialize responses service (always enabled)
	responsesService, err := responses.NewService(&config.Responses, router)
	if err != nil {
		logger.Warn("failed to initialize responses service", "error", err)
	} else {
		router.responsesService = responsesService
		logger.Info("initialized responses service")
	}

	// Initialize conversations service
	conversationsService, err := conversations.NewService(&config.Conversations)
	if err != nil {
		logger.Warn("failed to initialize conversations service", "error", err)
	} else {
		router.conversationsService = conversationsService
		logger.Info("initialized conversations service")
	}

	// Setup HTTP mux with auth middleware
	auth := middleware.Auth(config.Server.Token)
	router.mux = http.NewServeMux()
	router.mux.HandleFunc("/v1/models", auth(router.HandleModels))
	router.mux.HandleFunc("/v1/chat/completions", auth(router.HandleChatCompletions))
	router.mux.HandleFunc("/v1/embeddings", auth(router.HandleEmbeddings))
	router.mux.HandleFunc("/health", router.HandleHealth) // Health endpoint is not protected

	// Add responses endpoints if service is available
	if router.responsesService != nil {
		router.mux.HandleFunc("POST /v1/responses", auth(router.HandleCreateResponse))
		router.mux.HandleFunc("GET /v1/responses/{id}", auth(router.HandleGetResponse))
		router.mux.HandleFunc("DELETE /v1/responses/{id}", auth(router.HandleDeleteResponse))
		router.mux.HandleFunc("GET /v1/responses", auth(router.HandleListResponses))
		router.mux.HandleFunc("POST /v1/responses/{id}/cancel", auth(router.HandleCancelResponse))
		router.mux.HandleFunc("POST /v1/responses/compact", auth(router.HandleCompactResponses))
		router.mux.HandleFunc("GET /v1/responses/{id}/input-items", auth(router.HandleUnsupported))
		router.mux.HandleFunc("GET /v1/responses/{id}/input-tokens", auth(router.HandleUnsupported))
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
		logger.Info("conversations endpoints available")
	}

	// Add MCP endpoints if server is available
	if router.mcpServer != nil {
		router.mux.HandleFunc("/mcp", auth(router.HandleMCP))
		router.mux.HandleFunc("/mcp/discovery", auth(router.HandleMCPDiscovery))
		logger.Info("MCP server endpoints available at /mcp and /mcp/discovery")
	}

	// Add catch-all handler for unmatched routes (must be last)
	router.mux.HandleFunc("/", router.HandleCatchAll)

	return router, nil
}

func (r *Router) RefreshModels(ctx context.Context) error {
	r.logger.Info("refreshing models from all providers concurrently")

	// Clear existing model map with mutex protection
	r.ModelMapMu.Lock()
	r.ModelMap = make(map[string][]string)
	r.ModelMapMu.Unlock()

	modelSet := make(map[string]map[string]bool) // model -> provider -> exists
	var modelSetMu sync.Mutex

	// Use WaitGroup to fetch models from all healthy providers concurrently
	var wg sync.WaitGroup

	// First, add static models from providers with predefined model lists
	for providerName, provider := range r.Providers {
		if !provider.Enabled {
			continue
		}

		if provider.StaticModels {
			// Get static models from config
			var staticModels []string
			for _, providerConfig := range r.config.Providers {
				if providerConfig.Name == providerName {
					staticModels = providerConfig.Models
					break
				}
			}

			modelSetMu.Lock()
			for _, modelID := range staticModels {
				if shouldIncludeModel(modelID, provider.Allowlist, provider.Denylist) {
					if modelSet[modelID] == nil {
						modelSet[modelID] = make(map[string]bool)
					}
					modelSet[modelID][providerName] = true
				}
			}
			modelSetMu.Unlock()

			r.logger.Info("using static models from config",
				"provider", providerName,
				"count", len(staticModels))
		}
	}

	// Then, fetch dynamic models from providers without static lists
	for providerName, provider := range r.Providers {
		if !provider.Enabled || !provider.Healthy || provider.StaticModels {
			r.logger.Debug("skipping provider",
				"provider", providerName,
				"enabled", provider.Enabled,
				"healthy", provider.Healthy,
				"static_models", provider.StaticModels)
			continue
		}

		wg.Add(1)
		go func(name string, p *Provider) {
			defer wg.Done()

			r.logger.Debug("fetching models from provider", "provider", name, "base_url", p.BaseURL)

			// Use the timeout method for model fetching
			modelsResp, err := p.Client.ListModelsWithTimeout(ctx)
			if err != nil {
				r.logger.WithError(err).Error("failed to fetch models from provider", "provider", name)
				r.DisableProvider(name, fmt.Sprintf("model fetch failed: %v", err))
				return
			}

			// Mark provider as healthy since we successfully got models
			if !p.Healthy {
				r.EnableProvider(name)
			}

			// Log the models we found
			modelIDs := make([]string, 0, len(modelsResp.Data))
			for _, model := range modelsResp.Data {
				modelIDs = append(modelIDs, model.ID)
			}

			r.logger.Debug("fetched models from provider",
				"provider", name,
				"count", len(modelsResp.Data),
				"models", modelIDs)

			// Safely update the shared modelSet with filtering
			modelSetMu.Lock()
			for _, model := range modelsResp.Data {
				if shouldIncludeModel(model.ID, p.Allowlist, p.Denylist) {
					if modelSet[model.ID] == nil {
						modelSet[model.ID] = make(map[string]bool)
					}
					modelSet[model.ID][name] = true
				}
			}
			modelSetMu.Unlock()
		}(providerName, provider)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Build the final model map with mutex protection
	r.ModelMapMu.Lock()
	defer r.ModelMapMu.Unlock()

	for modelID, providers := range modelSet {
		providerNames := make([]string, 0, len(providers))
		for providerName := range providers {
			providerNames = append(providerNames, providerName)
		}
		r.ModelMap[modelID] = providerNames

		if len(providers) > 1 {
			r.logger.Debug("model available on multiple providers",
				"model", modelID,
				"providers", providerNames)
		}
	}

	r.logger.Info("model refresh complete",
		"total_models", len(r.ModelMap),
		"total_providers", len(r.Providers))

	return nil
}

// DisableProvider marks a provider as unhealthy and removes its models from the map
func (r *Router) DisableProvider(providerName, reason string) {
	r.ModelMapMu.Lock()
	defer r.ModelMapMu.Unlock()

	provider, exists := r.Providers[providerName]
	if !exists {
		return
	}

	if !provider.Healthy {
		return // Already disabled
	}

	provider.Healthy = false

	if provider.StaticModels {
		r.logger.Warn("static model provider disabled",
			"provider", providerName,
			"reason", reason,
			"static_models", true)
	} else {
		r.logger.Warn("provider disabled", "provider", providerName, "reason", reason)
	}

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

	if provider.Healthy {
		return // Already enabled
	}

	provider.Healthy = true
	r.logger.Info("provider re-enabled", "provider", providerName)
}

// shouldIncludeModel checks if a model should be included based on allowlist and denylist
func shouldIncludeModel(model string, allowlist, denylist []string) bool {
	// If denylist is provided, check if model is in it
	if len(denylist) > 0 {
		for _, denied := range denylist {
			if model == denied {
				return false
			}
		}
	}

	// If allowlist is provided, check if model is in it
	if len(allowlist) > 0 {
		for _, allowed := range allowlist {
			if model == allowed {
				return true
			}
		}
		// Model not found in allowlist, exclude it
		return false
	}

	// No allowlist, include model (assuming not denylisted)
	return true
}

func (r *Router) GetProvider(name string) interface{ GetNativeResponses() bool } {
	return r.Providers[name]
}

func (r *Router) GetProviderForModel(model string) (string, error) {
	r.ModelMapMu.RLock()
	providers, exists := r.ModelMap[model]
	r.ModelMapMu.RUnlock()

	if !exists {
		return "", fmt.Errorf("model %s not found in any provider", model)
	}

	if len(providers) == 1 {
		return providers[0], nil
	}

	// Find provider with least active completions
	var selectedProvider string
	minCompletions := int64(-1)

	for _, providerName := range providers {
		provider, exists := r.Providers[providerName]
		if !exists || !provider.Enabled {
			continue
		}

		if minCompletions == -1 || provider.ActiveCompletions < minCompletions {
			minCompletions = provider.ActiveCompletions
			selectedProvider = providerName
		}
	}

	if selectedProvider == "" {
		return "", fmt.Errorf("no enabled provider found for model %s", model)
	}

	return selectedProvider, nil
}

func (r *Router) ListModels() ModelsResponse {
	r.ModelMapMu.RLock()
	defer r.ModelMapMu.RUnlock()

	models := make([]Model, 0, len(r.ModelMap))
	for modelID := range r.ModelMap {
		models = append(models, Model{
			ID:      modelID,
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

func (r *Router) CreateChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Find provider for the model
	providerName, err := r.GetProviderForModel(req.Model)
	if err != nil {
		return nil, err
	}

	provider := r.Providers[providerName]

	// Increment active completions
	r.incrementActiveCompletions(providerName)
	defer r.decrementActiveCompletions(providerName)

	r.logger.Debug("routing chat completion", "model", req.Model, "provider", providerName)

	// Create token counter for usage estimation
	tokenCounter := openai.NewTokenCounter()
	// Convert messages to openai format for token counting
	openaiMessages := make([]openai.Message, len(req.Messages))
	for i, msg := range req.Messages {
		openaiMessages[i] = openai.Message{Role: msg.Role, Content: msg.Content}
	}
	tokenCounter.AddPromptTokensFromMessages(openaiMessages)

	// Make the request
	resp, err := provider.Client.CreateChatCompletion(ctx, req)
	if err != nil {
		// Check if this is a connection error and disable the provider
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}

	// Add completion tokens from response
	if len(resp.Choices) > 0 {
		openaiMsg := openai.Message{Role: resp.Choices[0].Message.Role, Content: resp.Choices[0].Message.Content}
		tokenCounter.AddCompletionTokensFromMessage(&openaiMsg)
	}

	// Inject usage if missing
	// Convert to openai format for usage injection
	openaiResp := &openai.ChatCompletionResponse{}
	tokenCounter.InjectUsageIfMissing(openaiResp)
	// Copy usage back to our response
	if openaiResp.Usage != nil && resp.Usage == nil {
		resp.Usage = &Usage{
			PromptTokens:     openaiResp.Usage.PromptTokens,
			CompletionTokens: openaiResp.Usage.CompletionTokens,
			TotalTokens:      openaiResp.Usage.TotalTokens,
		}
	}

	return resp, nil
}

func (r *Router) CreateEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	// Find provider for the model
	providerName, err := r.GetProviderForModel(req.Model)
	if err != nil {
		return nil, err
	}

	provider := r.Providers[providerName]

	r.logger.Info("routing embedding request", "model", req.Model, "provider", providerName)

	// Make the request
	resp, err := provider.Client.CreateEmbedding(ctx, req)
	if err != nil {
		// Check if this is a connection error and disable the provider
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}

	return resp, nil
}

func (r *Router) CreateChatCompletionRaw(ctx context.Context, req *ChatCompletionRequest) (*http.Response, string, error) {
	// Find provider for the model
	providerName, err := r.GetProviderForModel(req.Model)
	if err != nil {
		return nil, "", err
	}

	provider := r.Providers[providerName]

	// Increment active completions
	r.incrementActiveCompletions(providerName)

	// Create a deferred function to decrement completions
	defer func() {
		r.decrementActiveCompletions(providerName)
	}()

	r.logger.Debug("routing chat completion (raw)", "model", req.Model, "provider", providerName, "stream", req.Stream)

	// Make the raw request
	resp, err := provider.Client.CreateChatCompletionRaw(ctx, req)
	if err != nil {
		// Check if this is a connection error and disable the provider
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, "", err
	}

	// Return the response body as-is for pass-through
	return resp, providerName, nil
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
		"timeout",
		"dial",
		"EOF",
		"connection closed",
	}

	for _, pattern := range connectionPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	// Also detect fatal API errors that indicate a broken provider/model
	fatalAPIPatterns := []string{
		"missing tensor",                    // Corrupted GGUF file (Ollama)
		"llama runner process has terminated", // Model loading failure (Ollama)
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
		provider.ActiveCompletions++
	}
}

func (r *Router) decrementActiveCompletions(providerName string) {
	if provider, exists := r.Providers[providerName]; exists && provider.ActiveCompletions > 0 {
		provider.ActiveCompletions--
	}
}

// HTTP Handlers
func (r *Router) HandleModels(w http.ResponseWriter, req *http.Request) {
	// Update the models list and return it
	if err := r.RefreshModels(req.Context()); err != nil {
		r.logger.WithError(err).Error("failed to refresh models")
	}
	models := r.ListModels()

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, models); err != nil {
		r.logger.WithError(err).Error("failed to write models response")
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

		// Check if it's a model not found error
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

	// Create token counter for usage estimation
	tokenCounter := openai.NewTokenCounter()
	tokenCounter.AddPromptTokensFromMessages(completionReq.Messages)

	// Get raw response from provider
	resp, providerName, err := r.CreateChatCompletionRaw(ctx, completionReq)
	if err != nil {
		r.logger.WithError(err).Error("streaming chat completion failed")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// Copy headers from provider response
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set up to inject token usage at the end of stream
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Create flusher for SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		r.logger.Error("response writer does not support flushing")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Copy the streaming response to the client and inject usage when needed
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		// Check if this is a data line that needs modification
		if strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "data: [DONE]") {
			dataStr := strings.TrimPrefix(line, "data: ")
			var chunk ChatCompletionResponse

			if err := json.Unmarshal([]byte(dataStr), &chunk); err == nil && len(chunk.Choices) > 0 {
				// Convert delta to openai format for token counting
				openaiDelta := openai.Delta{Role: chunk.Choices[0].Delta.Role, Content: chunk.Choices[0].Delta.Content}
				tokenCounter.AddCompletionTokensFromDelta(&openaiDelta)

				// If this chunk has a finish_reason and no usage, inject our estimates
				if chunk.Choices[0].FinishReason == "stop" && chunk.Usage == nil {
					// Convert to openai format for usage injection
					openaiChunk := openai.ChatCompletionResponse{}
					tokenCounter.InjectUsageIfMissing(&openaiChunk)
					// Copy usage back to our chunk
					if openaiChunk.Usage != nil {
						chunk.Usage = &Usage{
							PromptTokens:     openaiChunk.Usage.PromptTokens,
							CompletionTokens: openaiChunk.Usage.CompletionTokens,
							TotalTokens:      openaiChunk.Usage.TotalTokens,
						}
					}
					modifiedJSON, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n", string(modifiedJSON))
				} else {
					// Pass through unchanged
					fmt.Fprintln(w, line)
				}
			} else {
				// Parse failed or no choices, pass through unchanged
				fmt.Fprintln(w, line)
			}
		} else {
			// Not a data line or is [DONE], pass through unchanged
			fmt.Fprintln(w, line)
		}

		if flusher != nil {
			flusher.Flush()
		}
	}

	r.logger.Debug("streaming response completed",
		"model", completionReq.Model,
		"provider", providerName)
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
			"healthy":            provider.Healthy,
			"active_completions": provider.ActiveCompletions,
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

// HandleMCP handles MCP protocol requests in native mode
func (r *Router) HandleMCP(w http.ResponseWriter, req *http.Request) {
	if r.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusServiceUnavailable)
		return
	}

	r.mcpServer.HandleRequest(w, req)
}

// HandleMCPDiscovery handles MCP protocol requests in discovery mode
func (r *Router) HandleMCPDiscovery(w http.ResponseWriter, req *http.Request) {
	if r.mcpServer == nil {
		http.Error(w, "MCP server not available", http.StatusServiceUnavailable)
		return
	}

	r.mcpServer.HandleDiscoveryRequest(w, req)
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

	// Find unhealthy providers (skip static model providers)
	for name, provider := range r.Providers {
		if provider.Enabled && !provider.Healthy && !provider.StaticModels {
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
			_, err := provider.Client.ListModels(ctx)
			if err != nil {
				r.logger.Debug("provider still unhealthy", "provider", name, "error", err)
				return
			}

			// Provider is healthy again, re-enable it
			r.EnableProvider(name)
			r.logger.Info("provider recovered and re-enabled", "provider", name)

			// Trigger a model refresh to add back this provider's models
			go func() {
				refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer refreshCancel()
				if err := r.RefreshModels(refreshCtx); err != nil {
					r.logger.WithError(err).Error("failed to refresh models after provider recovery", "provider", name)
				}
			}()
		}(providerName)
	}

	wg.Wait()
}

// ServeHTTP implements http.Handler
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Shutdown gracefully shuts down the router
func (r *Router) Shutdown() {
	r.shutdownOnce.Do(func() {
		close(r.shutdownChan)
		if r.responsesService != nil {
			r.responsesService.Close()
		}
		if r.conversationsService != nil {
			r.conversationsService.Close()
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

	resp, err := r.responsesService.CreateResponse(req.Context(), &createReq, nil) // Use default completion for API calls
	if err != nil {
		r.logger.WithError(err).Error("failed to create response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// log.PrettyJSON(createReq)
	// log.PrettyJSON(resp)

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

	// Parse query parameters
	filter := ResponseFilter{}
	if limitStr := req.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := parseIntParam(limitStr); err == nil {
			filter.Limit = limit
		}
	}
	filter.Order = req.URL.Query().Get("order")
	filter.After = req.URL.Query().Get("after")
	filter.Before = req.URL.Query().Get("before")

	resp, err := r.responsesService.ListResponses(req.Context(), filter)
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

	if err := r.responsesService.CompactResponses(req.Context()); err != nil {
		r.logger.WithError(err).Error("failed to compact responses")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]string{"status": "completed"}); err != nil {
		r.logger.WithError(err).Error("failed to write response")
	}
}

func (r *Router) HandleUnsupported(w http.ResponseWriter, req *http.Request) {
	http.Error(w, "Not supported", http.StatusNotFound)
}

// HandleCatchAll handles all unmatched routes and logs a warning
func (r *Router) HandleCatchAll(w http.ResponseWriter, req *http.Request) {
	r.logger.Warn("unhandled request", "method", req.Method, "path", req.URL.Path, "query", req.URL.RawQuery, "user_agent", req.Header.Get("User-Agent"))
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
