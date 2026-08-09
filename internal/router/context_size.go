package router

// defaultContextFloor is the final fallback context window (tokens) used when
// neither the model, its provider, nor the global config exposes one.
const defaultContextFloor = 4096

// resolveModelContext computes the context window for a single model served by
// a provider, applying the agreed precedence:
//
//  1. Explicit per-model override (provider.ModelContext) — the user knows best.
//  2. Discovered from the upstream API (Ollama /api/show via the ollama client,
//     Gemini inputTokenLimit) — surfaced through Model.ContextWindow.
//  3. Per-provider default (provider.DefaultContextSize).
//  4. Global default (config.Server.DefaultContextSize).
//  5. 4096 floor.
//
// Provider-specific discovery (Ollama's /api/show, Gemini's inputTokenLimit)
// lives in the mcp/ai provider clients, which populate Model.ContextWindow;
// this function is deliberately provider-agnostic.
func (r *Router) resolveModelContext(p *Provider, modelID string, discovered int) int {
	if p != nil {
		if v, ok := p.ModelContext[modelID]; ok && v > 0 {
			return v
		}
	}
	if discovered > 0 {
		return discovered
	}
	if p != nil && p.DefaultContextSize > 0 {
		return p.DefaultContextSize
	}
	if r.config != nil && r.config.Server.DefaultContextSize > 0 {
		return r.config.Server.DefaultContextSize
	}
	return defaultContextFloor
}

// resolvedContextLocked returns the context window to advertise for a model.
// The caller MUST hold ModelMapMu (at least RLock).
//
// Real models already have their resolved value in ModelContext (discovery or
// config). Virtual smart-router models (e.g. "auto") have no provider to
// discover from, so an undeclared router falls back to the global default and
// the 4096 floor — guaranteeing every served model reports a context window.
// A router can also declare an explicit context_size, which is written into
// ModelContext by the smart-router loader and takes precedence here.
func (r *Router) resolvedContextLocked(modelID string) int {
	if v := r.ModelContext[modelID]; v > 0 {
		return v
	}
	return r.resolveModelContext(nil, modelID, 0)
}
