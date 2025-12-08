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
		}

		router.Providers[provider.Name] = provider
		logger.Info("initialized provider", "name", provider.Name, "base_url", provider.BaseURL)
	}

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
				if modelSet[modelID] == nil {
					modelSet[modelID] = make(map[string]bool)
				}
				modelSet[modelID][providerName] = true
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

			// Safely update the shared modelSet
			modelSetMu.Lock()
			for _, model := range modelsResp.Data {
				if modelSet[model.ID] == nil {
					modelSet[model.ID] = make(map[string]bool)
				}
				modelSet[model.ID][name] = true
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

	r.logger.Info("routing chat completion", "model", req.Model, "provider", providerName)

	// Create token counter for usage estimation
	tokenCounter := openai.NewTokenCounter()
	tokenCounter.AddPromptTokensFromMessages(req.Messages)

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
		tokenCounter.AddCompletionTokensFromMessage(&resp.Choices[0].Message)
	}

	// Inject usage if missing
	tokenCounter.InjectUsageIfMissing(resp)

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

	r.logger.Info("routing chat completion (raw)", "model", req.Model, "provider", providerName, "stream", req.Stream)

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
	// Use the cached models list
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
			var chunk openai.ChatCompletionResponse

			if err := json.Unmarshal([]byte(dataStr), &chunk); err == nil && len(chunk.Choices) > 0 {
				tokenCounter.AddCompletionTokensFromDelta(&chunk.Choices[0].Delta)

				// If this chunk has a finish_reason and no usage, inject our estimates
				if chunk.Choices[0].FinishReason == "stop" && chunk.Usage == nil {
					tokenCounter.InjectUsageIfMissing(&chunk)
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
	writeJSON(w, health)
}

// Helper functions for JSON handling
func readJSON(req *http.Request, v interface{}) error {
	defer req.Body.Close()
	return json.NewDecoder(req.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}

// StartBackgroundTasks starts the background health check task
func (r *Router) StartBackgroundTasks() {
	r.wg.Add(1)
	go r.healthCheckTask()
}

// StopBackgroundTasks stops all background tasks
func (r *Router) StopBackgroundTasks() {
	close(r.shutdownChan)
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
