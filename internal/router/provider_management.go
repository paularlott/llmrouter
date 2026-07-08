package router

import (
	"context"
	"fmt"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
)

// loadStoredProviders loads all providers from KV storage and adds them
// to the router's Providers map. Called once at startup after config-file
// providers are loaded. Config-file providers take precedence — if a
// stored provider has the same name, it's skipped.
func (r *Router) loadStoredProviders(config *types.Config, logger Logger) {
	if r.providerStorage == nil {
		return
	}
	r.storedProviderNames = make(map[string]bool)

	stored, err := r.providerStorage.List(context.Background())
	if err != nil {
		logger.Warn("failed to list stored providers", "error", err)
		return
	}

	for _, sp := range stored {
		// Skip if a config-file provider already has this name
		if _, exists := r.Providers[sp.Name]; exists {
			logger.Debug("skipping stored provider — config provider with same name exists", "name", sp.Name)
			continue
		}

		if !sp.Enabled {
			logger.Debug("skipping disabled stored provider", "name", sp.Name)
			continue
		}

		if err := r.addStoredProvider(sp, logger); err != nil {
			logger.Warn("failed to load stored provider", "name", sp.Name, "error", err)
		} else {
			r.storedProviderNames[sp.Name] = true
		}
	}
}

// addStoredProvider creates an AI client for a stored provider config and
// adds it to the Providers map. Also triggers a model refresh for it.
func (r *Router) addStoredProvider(sp *storage.StoredProviderConfig, logger Logger) error {
	pc := types.ProviderConfig{
		Name:     sp.Name,
		Provider: sp.Provider,
		BaseURL:  sp.BaseURL,
		Token:    sp.Token,
		Enabled:  sp.Enabled,
		Weight:   sp.Weight,
		Models:   sp.Models,
		Tags:     sp.Tags,
	}

	client, err := newAIClient(&pc)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	weight := sp.Weight
	if weight <= 0 {
		weight = 1.0
	}

	providerType := sp.Provider
	if providerType == "" {
		providerType = "openai"
	}

	provider := &Provider{
		Name:           sp.Name,
		ProviderType:   providerType,
		Enabled:        sp.Enabled,
		Client:         client,
		Models:         sp.Models,
		ModelAllowlist: sp.ModelAllowlist,
		ModelDenylist:  sp.ModelDenylist,
		Weight:         weight,
		Tags:           sp.Tags,
		ModelTags:      sp.ModelTags,
		ModelAliases:   sp.ModelAliases,
	}
	provider.Healthy.Store(true)

	r.Providers[sp.Name] = provider
	logger.Info("loaded stored provider", "name", sp.Name, "type", providerType)
	return nil
}

// reloadProviders is called when a provider is created/updated/deleted via
// the admin UI. It removes all previously-loaded stored providers, re-reads
// them from storage, and refreshes the model map.
func (r *Router) reloadProviders() {
	if r.providerStorage == nil {
		return
	}

	// Remove all previously-loaded stored providers (keep config-file ones)
	// and clear their models from the map so disabled/deleted providers no
	// longer appear in the model list.
	for name := range r.storedProviderNames {
		delete(r.Providers, name)
		r.removeProviderModels(name)
	}
	r.storedProviderNames = make(map[string]bool)

	// Re-load from storage
	stored, err := r.providerStorage.List(context.Background())
	if err != nil {
		r.logger.Warn("failed to reload stored providers", "error", err)
		return
	}

	for _, sp := range stored {
		if _, exists := r.Providers[sp.Name]; exists {
			continue // config provider takes precedence
		}
		if !sp.Enabled {
			continue
		}
		if err := r.addStoredProvider(sp, r.logger); err != nil {
			r.logger.Warn("failed to reload stored provider", "name", sp.Name, "error", err)
		} else {
			r.storedProviderNames[sp.Name] = true
		}
	}

	// Refresh models from all providers in the background — don't block
	// the HTTP handler (can take 10+ seconds with slow providers).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.RefreshModels(ctx)
	}()
}
