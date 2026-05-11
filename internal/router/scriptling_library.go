package router

import (
	"context"
	"encoding/json"
	"math/rand"

	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/scriptling/evaluator"
	"github.com/paularlott/scriptling/object"
)

// buildRouterLibrary creates a stateless router library.
// Per-request data is read from the "request_json" env var set before eval.
// Output is written to "output_model" / "output_provider" env vars read after eval.
func buildRouterLibrary(r *Router) *object.Library {
	b := object.NewLibraryBuilder("router", "LLM Router - provider and model data for routing scripts")

	// reqData reads and parses request_json from the script environment.
	reqData := func(ctx context.Context) map[string]interface{} {
		env := evaluator.GetEnvFromContext(ctx)
		if env == nil {
			return nil
		}
		obj, ok := env.Get("request_json")
		if !ok {
			return nil
		}
		s, ok := obj.(*object.String)
		if !ok {
			return nil
		}
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(s.StringValue()), &m)
		return m
	}

	reqMsgs := func(ctx context.Context) []Message {
		m := reqData(ctx)
		if m == nil {
			return nil
		}
		raw, _ := json.Marshal(m["messages"])
		var msgs []Message
		_ = json.Unmarshal(raw, &msgs)
		return msgs
	}

	b.FunctionWithHelp("set_model", func(ctx context.Context, kwargs object.Kwargs, modelID string) {
		env := evaluator.GetEnvFromContext(ctx)
		if env == nil {
			return
		}
		env.Set("output_model", object.NewString(modelID))
		if hint := kwargs.MustGetString("hint", ""); hint != "" {
			env.Set("output_provider", object.NewString(hint))
		}
	}, "set_model(model_id, hint=provider) - Set the model to route to; hint optionally suggests a provider")

	b.FunctionWithHelp("get_request", func(ctx context.Context) map[string]interface{} {
		return reqData(ctx)
	}, "get_request() -> dict - Returns the current routing request (type, messages, tools)")

	b.FunctionWithHelp("is_chat_completion", func(ctx context.Context) bool {
		m := reqData(ctx)
		if m == nil {
			return false
		}
		t, _ := m["type"].(string)
		return t == reqTypeChat
	}, "is_chat_completion() -> bool - True if this is a chat completions request")

	b.FunctionWithHelp("is_responses", func(ctx context.Context) bool {
		m := reqData(ctx)
		if m == nil {
			return false
		}
		t, _ := m["type"].(string)
		return t == reqTypeResponses
	}, "is_responses() -> bool - True if this is a responses API request")

	b.FunctionWithHelp("message_content_types", func(ctx context.Context) []interface{} {
		return toAnySlice(messageContentTypes(reqMsgs(ctx)))
	}, "message_content_types() -> list - Unique content part types across all messages")

	b.FunctionWithHelp("total_tokens_estimate", func(ctx context.Context) int {
		tc := openai.NewTokenCounter()
		tc.AddPromptTokensFromMessages(reqMsgs(ctx))
		return tc.GetUsage().PromptTokens
	}, "total_tokens_estimate() -> int - Estimated prompt token count across all messages")

	b.FunctionWithHelp("system_prompt", func(ctx context.Context) string {
		for _, m := range reqMsgs(ctx) {
			if m.Role == "system" {
				if s, ok := m.Content.(string); ok {
					return s
				}
			}
		}
		return ""
	}, "system_prompt() -> str - Content of the system message, or empty string if none")

	b.FunctionWithHelp("last_message", func(ctx context.Context) string {
		msgs := reqMsgs(ctx)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" {
				if s, ok := msgs[i].Content.(string); ok {
					return s
				}
			}
		}
		return ""
	}, "last_message() -> str - Content of the last user message, or empty string if none")

	b.FunctionWithHelp("conversation_turns", func(ctx context.Context) int {
		turns := 0
		for _, m := range reqMsgs(ctx) {
			if m.Role == "user" {
				turns++
			}
		}
		return turns
	}, "conversation_turns() -> int - Number of user turns in the conversation")

	b.FunctionWithHelp("models_by_tags", func(tags []string) []interface{} {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		var result []interface{}
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

	b.FunctionWithHelp("providers_for_model", func(modelID string) []interface{} {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		names, ok := r.ModelMap[modelID]
		if !ok {
			return []interface{}{}
		}
		result := make([]interface{}, 0, len(names))
		for _, name := range names {
			p, ok := r.Providers[name]
			if !ok || !p.Enabled || !p.Healthy {
				continue
			}
			result = append(result, map[string]interface{}{
				"name":   name,
				"type":   p.ProviderType,
				"load":   p.ActiveCompletions.Load(),
				"weight": p.Weight,
				"tags":   toAnySlice(p.Tags),
			})
		}
		return result
	}, "providers_for_model(model_id) -> list[dict] - Healthy providers serving a model")

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
	}, "random_model(tag) -> str - Weighted random model with the given tag")

	b.FunctionWithHelp("providers", func(kwargs object.Kwargs) []interface{} {
		filterTag := kwargs.MustGetString("tag", "")
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		providerModels := make(map[string][]string)
		for modelID, names := range r.ModelMap {
			for _, name := range names {
				providerModels[name] = append(providerModels[name], modelID)
			}
		}
		result := make([]interface{}, 0, len(r.Providers))
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

	b.FunctionWithHelp("models_for_provider", func(kwargs object.Kwargs, providerName string) []interface{} {
		filterTag := kwargs.MustGetString("tag", "")
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		p := r.Providers[providerName]
		var result []interface{}
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

	b.FunctionWithHelp("models_by_tag", func(tag string) []interface{} {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		var result []interface{}
		for modelID, tags := range r.ModelTags {
			if hasTag(tags, tag) {
				result = append(result, modelID)
			}
		}
		return result
	}, "models_by_tag(tag) -> list - Returns model IDs that have the given tag")

	b.FunctionWithHelp("model_tags", func(modelID string) []interface{} {
		r.ModelMapMu.RLock()
		defer r.ModelMapMu.RUnlock()
		if tags, ok := r.ModelTags[modelID]; ok {
			return toAnySlice(tags)
		}
		return []interface{}{}
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

	b.FunctionWithHelp("provider_healthy", func(providerName string) bool {
		p, ok := r.Providers[providerName]
		return ok && p.Enabled && p.Healthy
	}, "provider_healthy(name) -> bool - True if the provider exists, is enabled, and is healthy")

	b.FunctionWithHelp("has_provider", func(providerName string) bool {
		_, ok := r.Providers[providerName]
		return ok
	}, "has_provider(name) -> bool - True if the provider exists in config (regardless of health)")

	b.FunctionWithHelp("model_load", func(modelID string) int64 {
		r.ModelMapMu.RLock()
		names := r.ModelMap[modelID]
		r.ModelMapMu.RUnlock()
		var total int64
		for _, name := range names {
			if p, ok := r.Providers[name]; ok && p.Enabled && p.Healthy {
				total += p.ActiveCompletions.Load()
			}
		}
		return total
	}, "model_load(model_id) -> int - Total active completions across all healthy providers serving the model")

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
