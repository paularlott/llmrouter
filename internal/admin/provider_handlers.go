package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/paularlott/llmrouter/internal/storage"
)

// ProviderDetail is the full provider config returned by the management API.
// For static (config-file) providers, only the display fields are populated.
// For stored providers, all editable fields are included.
type ProviderDetail struct {
	Name           string              `json:"name"`
	Provider       string              `json:"provider"`
	BaseURL        string              `json:"base_url,omitempty"`
	Enabled        bool                `json:"enabled"`
	Healthy        bool                `json:"healthy"`
	Weight         float64             `json:"weight"`
	ModelCount     int                 `json:"model_count"`
	Models         []string            `json:"models,omitempty"`
	ModelAllowlist []string            `json:"model_allowlist,omitempty"`
	Tags           []string            `json:"tags,omitempty"`
	ModelDenylist  []string            `json:"model_denylist,omitempty"`
	ModelAliases   map[string]string   `json:"model_aliases,omitempty"`
	ModelTags      map[string][]string `json:"model_tags,omitempty"`
	StaticProvider bool                `json:"static_provider"`
}

// HandleProvidersPage renders the providers management page.
func (a *Admin) HandleProvidersPage(w http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "providers.html", data)
}

// HandleListProviders returns all providers (static + stored) for the
// management page. Static providers are read-only; stored providers
// support full CRUD.
func (a *Admin) HandleListProviders(w http.ResponseWriter, r *http.Request) {
	result := make([]ProviderDetail, 0)

	// Static providers from runtime (config-file based)
	staticNames := make(map[string]bool)
	if a.getProviders != nil {
		for _, p := range a.getProviders() {
			if p.StaticProvider {
				staticNames[p.Name] = true
			}
			result = append(result, ProviderDetail{
				Name:           p.Name,
				Provider:       p.Type,
				Enabled:        true,
				Healthy:        p.Healthy,
				Weight:         p.Weight,
				ModelCount:     p.ModelCount,
				StaticProvider: p.StaticProvider,
			})
		}
	}

	// Stored providers (from KV)
	if a.providerStorage != nil {
		stored, err := a.providerStorage.List(r.Context())
		if err == nil {
			for _, sp := range stored {
				if staticNames[sp.Name] {
					continue // static config takes precedence
				}
				weight := sp.Weight
				if weight <= 0 {
					weight = 1.0
				}
				detail := ProviderDetail{
					Name:           sp.Name,
					Provider:       sp.Provider,
					BaseURL:        sp.BaseURL,
					Enabled:        sp.Enabled,
					Weight:         weight,
					Models:         sp.Models,
					ModelAllowlist: sp.ModelAllowlist,
					Tags:           sp.Tags,
					ModelDenylist:  sp.ModelDenylist,
					ModelAliases:   sp.ModelAliases,
					ModelTags:      sp.ModelTags,
					StaticProvider: false,
				}
				// Enrich with runtime health/model count if available
				if a.getProviders != nil {
					for _, rp := range a.getProviders() {
						if rp.Name == sp.Name {
							detail.Healthy = rp.Healthy
							detail.ModelCount = rp.ModelCount
							break
						}
					}
				}
				result = append(result, detail)
			}
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleGetProvider returns a single provider's config. Checks storage first,
// then falls back to static config from runtime info.
func (a *Admin) HandleGetProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	// Check storage first
	if a.providerStorage != nil {
		sp, err := a.providerStorage.Get(r.Context(), name)
		if err == nil {
			weight := sp.Weight
			if weight <= 0 {
				weight = 1.0
			}
			writeJSON(w, http.StatusOK, ProviderDetail{
				Name:           sp.Name,
				Provider:       sp.Provider,
				BaseURL:        sp.BaseURL,
				Enabled:        sp.Enabled,
				Weight:         weight,
				Models:         sp.Models,
				ModelAllowlist: sp.ModelAllowlist,
				Tags:           sp.Tags,
				ModelDenylist:  sp.ModelDenylist,
				ModelAliases:   sp.ModelAliases,
				ModelTags:      sp.ModelTags,
				StaticProvider: false,
			})
			return
		}
	}

	// Fall back to runtime info (static provider)
	if a.getProviders != nil {
		for _, p := range a.getProviders() {
			if p.Name == name {
				writeJSON(w, http.StatusOK, ProviderDetail{
					Name:           p.Name,
					Provider:       p.Type,
					Enabled:        true,
					Healthy:        p.Healthy,
					Weight:         p.Weight,
					ModelCount:     p.ModelCount,
					StaticProvider: true,
				})
				return
			}
		}
	}

	writeError(w, http.StatusNotFound, "provider not found")
}

// HandleCreateProvider creates a new provider in storage.
func (a *Admin) HandleCreateProvider(w http.ResponseWriter, r *http.Request) {
	if a.providerStorage == nil || !a.providerStorageWritable {
		writeError(w, http.StatusBadRequest, "provider storage requires a configured storage path")
		return
	}

	var req struct {
		Name           string              `json:"name"`
		Provider       string              `json:"provider"`
		BaseURL        string              `json:"base_url"`
		Token          string              `json:"token"`
		Enabled        bool                `json:"enabled"`
		Weight         float64             `json:"weight"`
		Models         []string            `json:"models"`
		ModelAllowlist []string            `json:"model_allowlist"`
		Tags           []string            `json:"tags"`
		ModelDenylist  []string            `json:"model_denylist"`
		ModelAliases   map[string]string   `json:"model_aliases"`
		ModelTags      map[string][]string `json:"model_tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Provider == "" {
		req.Provider = "openai"
	}
	if req.Weight <= 0 {
		req.Weight = 1.0
	}

	provider := &storage.StoredProviderConfig{
		Name:           req.Name,
		Provider:       req.Provider,
		BaseURL:        strings.TrimSuffix(req.BaseURL, "/"),
		Token:          req.Token,
		Enabled:        req.Enabled,
		Weight:         req.Weight,
		Models:         req.Models,
		ModelAllowlist: req.ModelAllowlist,
		Tags:           req.Tags,
		ModelDenylist:  req.ModelDenylist,
		ModelAliases:   req.ModelAliases,
		ModelTags:      req.ModelTags,
	}

	if err := a.providerStorage.Create(r.Context(), provider); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if a.onProviderChange != nil {
		a.onProviderChange()
	}

	writeJSON(w, http.StatusCreated, ProviderDetail{
		Name:           provider.Name,
		Provider:       provider.Provider,
		BaseURL:        provider.BaseURL,
		Enabled:        provider.Enabled,
		Weight:         provider.Weight,
		StaticProvider: false,
	})
}

// HandleUpdateProvider updates a stored provider.
func (a *Admin) HandleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	if a.providerStorage == nil || !a.providerStorageWritable {
		writeError(w, http.StatusBadRequest, "provider storage requires a configured storage path")
		return
	}

	provider, err := a.providerStorage.Get(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider not found or is a static config provider")
		return
	}

	var req struct {
		Provider       string              `json:"provider"`
		BaseURL        string              `json:"base_url"`
		Token          string              `json:"token"`
		Enabled        bool                `json:"enabled"`
		Weight         float64             `json:"weight"`
		Models         []string            `json:"models"`
		ModelAllowlist []string            `json:"model_allowlist"`
		Tags           []string            `json:"tags"`
		ModelDenylist  []string            `json:"model_denylist"`
		ModelAliases   map[string]string   `json:"model_aliases"`
		ModelTags      map[string][]string `json:"model_tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Provider != "" {
		provider.Provider = req.Provider
	}
	if req.BaseURL != "" {
		provider.BaseURL = strings.TrimSuffix(req.BaseURL, "/")
	}
	if req.Token != "" {
		provider.Token = req.Token
	}
	provider.Enabled = req.Enabled
	if req.Weight > 0 {
		provider.Weight = req.Weight
	}
	// Only overwrite list/map fields when the request actually includes
	// them. The UI edit form doesn't send model_allowlist, model_denylist,
	// model_aliases, model_tags, or tags — without these guards, editing
	// any field (e.g. weight) would silently wipe them.
	if req.Models != nil {
		provider.Models = req.Models
	}
	if req.ModelAllowlist != nil {
		provider.ModelAllowlist = req.ModelAllowlist
	}
	if req.Tags != nil {
		provider.Tags = req.Tags
	}
	if req.ModelDenylist != nil {
		provider.ModelDenylist = req.ModelDenylist
	}
	if req.ModelAliases != nil {
		provider.ModelAliases = req.ModelAliases
	}
	if req.ModelTags != nil {
		provider.ModelTags = req.ModelTags
	}

	if err := a.providerStorage.Update(r.Context(), provider); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update provider")
		return
	}

	if a.onProviderChange != nil {
		a.onProviderChange()
	}

	writeJSON(w, http.StatusOK, ProviderDetail{
		Name:           provider.Name,
		Provider:       provider.Provider,
		BaseURL:        provider.BaseURL,
		Enabled:        provider.Enabled,
		Weight:         provider.Weight,
		StaticProvider: false,
	})
}

// HandleDeleteProvider deletes a stored provider.
func (a *Admin) HandleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	if a.providerStorage == nil || !a.providerStorageWritable {
		writeError(w, http.StatusBadRequest, "provider storage requires a configured storage path")
		return
	}

	if err := a.providerStorage.Delete(r.Context(), name); err != nil {
		writeError(w, http.StatusNotFound, "provider not found or is a static config provider")
		return
	}

	if a.onProviderChange != nil {
		a.onProviderChange()
	}

	w.WriteHeader(http.StatusNoContent)
}
