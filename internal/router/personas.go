package router

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/lmchatkit"

	cli "github.com/paularlott/cli"
	cli_toml "github.com/paularlott/cli/toml"
)

// personaEntry is the router's internal merged persona record. It adds a
// Static flag so the admin UI can show config-file personas as read-only the
// same way it shows config-file providers.
type personaEntry struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	DefaultModel string                 `json:"default_model,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"`
	Static       bool                   `json:"static"` // true = from .toml file (read-only), false = from KV storage
}

// mergedPersonaSource implements lmchatkit.PersonaSource by combining:
//   - the built-in "Default" persona (always present),
//   - .toml files read from a configured PersonasDir (read on each call so
//     edits land without a restart), and
//   - personas stored in KV via PersonaStorage (managed by the admin UI).
//
// It replaces lmchatkit's built-in file watcher so config-file and stored
// personas share a single source of truth surfaced to both /api/personas and
// the admin page.
type mergedPersonaSource struct {
	dir     string
	storage storage.PersonaStorage
}

// Personas satisfies [lmchatkit.PersonaSource]. Called on every /api/personas
// request by lmchatkit, so a fresh merge keeps both chat and admin current.
func (m *mergedPersonaSource) Personas(ctx context.Context) ([]lmchatkit.Persona, error) {
	entries := m.entries(ctx)
	out := make([]lmchatkit.Persona, 0, len(entries))
	for _, e := range entries {
		out = append(out, lmchatkit.Persona{
			ID:           e.ID,
			Name:         e.Name,
			Description:  e.Description,
			SystemPrompt: e.SystemPrompt,
			DefaultModel: e.DefaultModel,
			Params:       e.Params,
		})
	}
	return out, nil
}

// infos returns the merged set as admin.PersonaInfo for the management page.
// Called by the router's getPersonas callback.
func (m *mergedPersonaSource) infos(ctx context.Context) []admin.PersonaInfo {
	entries := m.entries(ctx)
	out := make([]admin.PersonaInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, admin.PersonaInfo{
			ID:           e.ID,
			Name:         e.Name,
			Description:  e.Description,
			SystemPrompt: e.SystemPrompt,
			DefaultModel: e.DefaultModel,
			Params:       e.Params,
			Static:       e.Static,
		})
	}
	return out
}

// entries builds the merged, name-sorted persona list. The Default persona is
// always first by convention. Duplicate names between file and stored sources
// are allowed (they keep distinct IDs); config-file personas are NOT treated
// as authoritative the way config-file providers are, because personas have no
// runtime health/state to shadow.
func (m *mergedPersonaSource) entries(ctx context.Context) []personaEntry {
	out := []personaEntry{{ID: "default", Name: "Default"}}

	// File-backed personas (read-only in the UI).
	for _, e := range readFilePersonas(m.dir) {
		e.Static = true
		out = append(out, e)
	}

	// Stored personas (managed via UI).
	if m.storage != nil {
		stored, err := m.storage.List(ctx)
		if err == nil {
			for _, p := range stored {
				out = append(out, personaEntry{
					ID:           p.ID,
					Name:         p.Name,
					Description:  p.Description,
					SystemPrompt: p.SystemPrompt,
					DefaultModel: p.DefaultModel,
					Params:       p.Params,
					Static:       false,
				})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readFilePersonas parses every .toml in dir. A missing/unreadable dir yields
// nil (the Default persona is still offered by the caller). Malformed files
// are skipped rather than failing the lot — matches lmchatkit's behaviour.
func readFilePersonas(dir string) []personaEntry {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []personaEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		p, err := loadPersonaFile(path)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// loadPersonaFile parses one persona TOML. The ID is derived from the filename
// stem via stableID so saved conversations keep referencing the persona even
// if its display name changes — identical scheme to lmchatkit's file loader, so
// IDs stay stable whether personas are served from the dir or the KV store.
func loadPersonaFile(path string) (personaEntry, error) {
	base := cli_toml.NewConfigFile(&path, func() []string { return []string{filepath.Dir(path)} })
	cfg := cli.NewTypedConfigFile(base)
	if err := cfg.LoadData(); err != nil {
		return personaEntry{}, fmt.Errorf("parse %s: %w", path, err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".toml")
	p := personaEntry{
		ID:           stableID(stem),
		Name:         cfg.GetString("name"),
		Description:  cfg.GetString("description"),
		SystemPrompt: cfg.GetString("system_prompt"),
		DefaultModel: cfg.GetString("default_model"),
	}
	if p.Name == "" {
		p.Name = stem
	}
	if v, ok := cfg.GetValue("params"); ok {
		if m, ok := v.(map[string]any); ok {
			p.Params = m
		}
	}
	return p, nil
}

// stableID hashes a stem into a short, URL-safe identifier. Matches lmchatkit's
// internal scheme (first 8 hex of sha256) so IDs are consistent across
// file-backed and stored personas and survive renames.
func stableID(stem string) string {
	sum := sha256.Sum256([]byte(stem))
	return hex.EncodeToString(sum[:])[:8]
}
