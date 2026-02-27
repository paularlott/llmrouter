package router

import (
	"encoding/json"
	"math/rand"

	"github.com/paularlott/scriptling/object"
)

// buildRouterLibraryForRequest creates the library bound to a specific request.
// setModel is called when the script calls router.set_model(model_id).
func buildRouterLibraryForRequest(r *Router, reqJSON string, reqType string, msgs []Message, setModel func(string)) *object.Library {
	b := object.NewLibraryBuilder("router", "LLM Router - provider and model data for routing scripts")

	b.FunctionWithHelp("set_model", func(modelID string) {
		setModel(modelID)
	}, "set_model(model_id) - Set the model to route to; provider selected automatically")

	b.FunctionWithHelp("get_request", func() map[string]interface{} {
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(reqJSON), &m)
		return m
	}, "get_request() -> dict - Returns the current routing request (type, messages, tools)")

	b.FunctionWithHelp("is_chat_completion", func() bool {
		return reqType == reqTypeChat
	}, "is_chat_completion() -> bool - True if this is a chat completions request")

	b.FunctionWithHelp("is_responses", func() bool {
		return reqType == reqTypeResponses
	}, "is_responses() -> bool - True if this is a responses API request")

	b.FunctionWithHelp("message_content_types", func() []string {
		return messageContentTypes(msgs)
	}, "message_content_types() -> list - Unique content part types across all messages (e.g. 'text', 'image_url')")

	b.FunctionWithHelp("total_tokens_estimate", func() int {
		total := 0
		for _, m := range msgs {
			if s, ok := m.Content.(string); ok {
				total += len(s) / 4
			}
		}
		return total
	}, "total_tokens_estimate() -> int - Rough token estimate (chars/4) across all messages")

	b.FunctionWithHelp("models_by_tags", func(tags []string) []string {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()

		var result []string
		outer:
		for modelID, modelTags := range r.ModelTags {
			for _, tag := range tags {
				if !hasTag(modelTags, tag) {
					continue outer
				}
			}
			result = append(result, modelID)
		}
		return result
	}, "models_by_tags(tags) -> list - Model IDs that have ALL of the given tags")

	b.FunctionWithHelp("provider_for_model", func(modelID string) string {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		if names, ok := r.ModelMap[modelID]; ok && len(names) > 0 {
			return names[0]
		}
		return ""
	}, "provider_for_model(model_id) -> str - Provider name for a model (first if multiple, empty if not found)")

	b.FunctionWithHelp("random_model", func(tag string) string {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()

		type candidate struct {
			modelID string
			weight  float64
		}
		var pool []candidate
		for modelID, tags := range r.ModelTags {
			if !hasTag(tags, tag) {
				continue
			}
			w := 1.0
			if names, ok := r.ModelMap[modelID]; ok {
				for _, name := range names {
					if p, ok := r.Providers[name]; ok && p.Enabled && p.Healthy {
						w = p.Weight
						break
					}
				}
			}
			pool = append(pool, candidate{modelID, w})
		}
		if len(pool) == 0 {
			return ""
		}
		total := 0.0
		for _, c := range pool {
			total += c.weight
		}
		r2 := rand.Float64() * total
		for _, c := range pool {
			r2 -= c.weight
			if r2 <= 0 {
				return c.modelID
			}
		}
		return pool[len(pool)-1].modelID
	}, "random_model(tag) -> str - Weighted random model with the given tag (empty string if none)")

	b.FunctionWithHelp("providers", func(kwargs object.Kwargs) []map[string]interface{} {
		filterTag := kwargs.MustGetString("tag", "")

		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()

		providerModels := make(map[string][]string)
		for modelID, names := range r.ModelMap {
			for _, name := range names {
				providerModels[name] = append(providerModels[name], modelID)
			}
		}

		result := make([]map[string]interface{}, 0, len(r.Providers))
		for name, p := range r.Providers {
			if !p.Enabled || !p.Healthy {
				continue
			}
			if filterTag != "" && !hasTag(p.Tags, filterTag) {
				continue
			}
			models := providerModels[name]
			if models == nil {
				models = []string{}
			}
			result = append(result, map[string]interface{}{
				"name":   name,
				"type":   p.ProviderType,
				"load":   p.ActiveCompletions.Load(),
				"weight": p.Weight,
				"tags":   toAnySlice(p.Tags),
				"models": toAnySlice(models),
			})
		}
		return result
	}, "providers(**kwargs) -> list - Healthy providers. Optional: tag=str to filter by provider tag")

	b.FunctionWithHelp("models_for_provider", func(kwargs object.Kwargs, providerName string) []string {
		filterTag := kwargs.MustGetString("tag", "")

		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()

		p := r.Providers[providerName]
		var result []string
		for modelID, names := range r.ModelMap {
			for _, name := range names {
				if name != providerName {
					continue
				}
				if filterTag != "" {
					var tags []string
					if p != nil {
						tags = p.ModelTags[modelID]
					}
					if len(tags) == 0 {
						tags = r.ModelTags[modelID]
					}
					if !hasTag(tags, filterTag) {
						continue
					}
				}
				result = append(result, modelID)
				break
			}
		}
		return result
	}, "models_for_provider(name, **kwargs) -> list - Models for a provider. Optional: tag=str to filter by model tag")

	b.FunctionWithHelp("models_by_tag", func(tag string) []string {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()

		var result []string
		for modelID, tags := range r.ModelTags {
			if hasTag(tags, tag) {
				result = append(result, modelID)
			}
		}
		return result
	}, "models_by_tag(tag) -> list - Returns model IDs that have the given tag")

	b.FunctionWithHelp("model_tags", func(modelID string) []string {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		if tags, ok := r.ModelTags[modelID]; ok {
			return tags
		}
		return []string{}
	}, "model_tags(model_id) -> list - Returns tags for a model")

	b.FunctionWithHelp("has_model", func(modelID string) bool {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		_, ok := r.ModelMap[modelID]
		return ok
	}, "has_model(model_id) -> bool - True if the model is available from any provider")

	b.FunctionWithHelp("provider_load", func(providerName string) int64 {
		if p, ok := r.Providers[providerName]; ok {
			return p.ActiveCompletions.Load()
		}
		return -1
	}, "provider_load(name) -> int - Active completions for a provider (-1 if not found)")

	return b.Build()
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func toAnySlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
