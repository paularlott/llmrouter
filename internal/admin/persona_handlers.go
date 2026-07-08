package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/paularlott/llmrouter/internal/storage"
)

// HandlePersonasPage renders the personas management page.
func (a *Admin) HandlePersonasPage(w http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		CSSFile: "/admin/assets/main.css",
		JSFile:  "/admin/assets/main.js",
	}
	a.templates.Render(w, "personas.html", data)
}

// HandleListPersonas returns all personas (file-based + stored) for the
// management page. File-based personas are read-only; stored personas support
// full CRUD. Merging happens in the router's persona source so chat and admin
// see the same set.
func (a *Admin) HandleListPersonas(w http.ResponseWriter, r *http.Request) {
	if a.getPersonas == nil {
		writeJSON(w, http.StatusOK, []PersonaInfo{})
		return
	}
	writeJSON(w, http.StatusOK, a.getPersonas())
}

// HandleGetPersona returns a single persona by ID. Checks the merged list from
// the router (file + stored) so a GET works regardless of source.
func (a *Admin) HandleGetPersona(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}

	if a.getPersonas != nil {
		for _, p := range a.getPersonas() {
			if p.ID == id {
				writeJSON(w, http.StatusOK, p)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "persona not found")
}

// HandleCreatePersona creates a new persona in storage. The ID is derived
// from the name (sha256[:8]) so it's stable, URL-safe, and survives renames.
func (a *Admin) HandleCreatePersona(w http.ResponseWriter, r *http.Request) {
	if a.personaStorage == nil || !a.personaStorageWritable {
		writeError(w, http.StatusBadRequest, "persona storage requires a configured storage path")
		return
	}

	var req struct {
		Name         string                 `json:"name"`
		Description  string                 `json:"description"`
		SystemPrompt string                 `json:"system_prompt"`
		DefaultModel string                 `json:"default_model"`
		Params       map[string]interface{} `json:"params"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	persona := &storage.StoredPersona{
		ID:           personaIDFromName(name),
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		SystemPrompt: req.SystemPrompt,
		DefaultModel: strings.TrimSpace(req.DefaultModel),
		Params:       req.Params,
	}

	if err := a.personaStorage.Create(r.Context(), persona); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if a.onPersonaChange != nil {
		a.onPersonaChange()
	}

	writeJSON(w, http.StatusCreated, PersonaInfo{
		ID:           persona.ID,
		Name:         persona.Name,
		Description:  persona.Description,
		SystemPrompt: persona.SystemPrompt,
		DefaultModel: persona.DefaultModel,
		Params:       persona.Params,
		Static:       false,
	})
}

// HandleUpdatePersona updates a stored persona. File-based personas are
// immutable from the UI — attempting to update one returns 404 so the caller
// surfaces a clear error rather than silently no-op'ing.
func (a *Admin) HandleUpdatePersona(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}

	if a.personaStorage == nil || !a.personaStorageWritable {
		writeError(w, http.StatusBadRequest, "persona storage requires a configured storage path")
		return
	}

	persona, err := a.personaStorage.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "persona not found or is a config-file persona")
		return
	}

	var req struct {
		Name         string                 `json:"name"`
		Description  string                 `json:"description"`
		SystemPrompt string                 `json:"system_prompt"`
		DefaultModel string                 `json:"default_model"`
		Params       map[string]interface{} `json:"params"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// The ID stays anchored to the original name so saved conversations keep
	// resolving; only display fields are editable.
	if name := strings.TrimSpace(req.Name); name != "" {
		persona.Name = name
	}
	persona.Description = strings.TrimSpace(req.Description)
	persona.SystemPrompt = req.SystemPrompt
	persona.DefaultModel = strings.TrimSpace(req.DefaultModel)
	persona.Params = req.Params

	if err := a.personaStorage.Update(r.Context(), persona); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update persona")
		return
	}

	if a.onPersonaChange != nil {
		a.onPersonaChange()
	}

	writeJSON(w, http.StatusOK, PersonaInfo{
		ID:           persona.ID,
		Name:         persona.Name,
		Description:  persona.Description,
		SystemPrompt: persona.SystemPrompt,
		DefaultModel: persona.DefaultModel,
		Params:       persona.Params,
		Static:       false,
	})
}

// HandleDeletePersona deletes a stored persona. File-based personas cannot be
// deleted from the UI.
func (a *Admin) HandleDeletePersona(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}

	if a.personaStorage == nil || !a.personaStorageWritable {
		writeError(w, http.StatusBadRequest, "persona storage requires a configured storage path")
		return
	}

	if err := a.personaStorage.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "persona not found or is a config-file persona")
		return
	}

	if a.onPersonaChange != nil {
		a.onPersonaChange()
	}

	w.WriteHeader(http.StatusNoContent)
}

// personaIDFromName derives a stable 8-hex-char ID from a persona name.
// Matches lmchatkit's filename-stem hashing so IDs are uniform across sources.
func personaIDFromName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:8]
}
