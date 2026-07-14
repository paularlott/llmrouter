package router

import (
	"context"
	"fmt"
	"sync"

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
		configSig:      storedProviderSig(sp),
	}
	provider.Healthy.Store(true)

	r.Providers[sp.Name] = provider
	logger.Info("loaded stored provider", "name", sp.Name, "type", providerType)
	return nil
}

// reloadProviders is called when a provider is created/updated/deleted/toggled
// via the admin UI. It reconciles the loaded stored providers against storage
// differentially: only providers that are gone, disabled, or whose config
// changed are removed and reloaded. Unchanged providers keep their client and
// their models, so editing or toggling one provider never blanks another's
// model count.
func (r *Router) reloadProviders() {
	if r.providerStorage == nil {
		return
	}

	stored, err := r.providerStorage.List(context.Background())
	if err != nil {
		r.logger.Warn("failed to reload stored providers", "error", err)
		return
	}

	// Index the desired state by name.
	desired := make(map[string]*storage.StoredProviderConfig, len(stored))
	for _, sp := range stored {
		desired[sp.Name] = sp
	}

	// Remove currently-loaded stored providers that are gone, disabled, or
	// whose config changed (the changed ones are re-added below). Unchanged
	// providers are left fully intact, including their models.
	for name := range r.storedProviderNames {
		sp, want := desired[name]
		existing := r.Providers[name]
		if !want || !sp.Enabled {
			delete(r.Providers, name)
			r.removeProviderModels(name)
			delete(r.storedProviderNames, name)
			continue
		}
		if existing != nil && existing.configSig == storedProviderSig(sp) {
			continue // unchanged — keep client + models
		}
		// config changed — drop it so it's rebuilt below
		delete(r.Providers, name)
		r.removeProviderModels(name)
		delete(r.storedProviderNames, name)
	}

	// Add providers that are enabled but not currently loaded: new,
	// newly-enabled, or rebuilt because their config changed above.
	needFetch := make(map[string]*Provider)
	for name, sp := range desired {
		if !sp.Enabled {
			continue
		}
		if r.storedProviderNames[name] {
			continue
		}
		if _, exists := r.Providers[name]; exists {
			continue // config-file provider takes precedence
		}
		if err := r.addStoredProvider(sp, r.logger); err != nil {
			r.logger.Warn("failed to reload stored provider", "name", sp.Name, "error", err)
			continue
		}
		r.storedProviderNames[name] = true
		if p := r.Providers[name]; p != nil {
			needFetch[name] = p
		}
	}

	if len(needFetch) == 0 {
		return
	}

	// Fetch models only for providers that were (re)loaded. Unchanged
	// providers already have their models and are not re-fetched.
	go func() {
		var wg sync.WaitGroup
		for name, p := range needFetch {
			wg.Add(1)
			go func(n string, pp *Provider) {
				defer wg.Done()
				r.fetchProviderModels(n, pp)
			}(name, p)
		}
		wg.Wait()

		if r.smartRouters != nil {
			r.ModelMapMu.RLock()
			r.smartRouters.reconcileCollisions(r.ModelMap)
			r.ModelMapMu.RUnlock()
		}
	}()
}

// storedProviderSig returns a signature covering the StoredProviderConfig
// fields that determine the client and its model set. Two configs with the
// same signature need no reload; a difference forces a rebuild + model refetch.
func storedProviderSig(sp *storage.StoredProviderConfig) string {
	return fmt.Sprintf("%s\x1f%s\x1f%s\x1f%v\x1f%v\x1f%v\x1f%v\x1f%v\x1f%v\x1f%v",
		sp.Provider, sp.BaseURL, sp.Token, sp.Weight,
		sp.Models, sp.ModelAllowlist, sp.ModelDenylist,
		sp.Tags, sp.ModelTags, sp.ModelAliases,
	)
}
