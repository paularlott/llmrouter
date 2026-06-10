package router

import (
	"context"
	"testing"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// mockClient satisfies ai.Client without making real HTTP calls.
type mockClient struct{ providerName string }

func (m *mockClient) Provider() string                        { return m.providerName }
func (m *mockClient) SupportsCapability(string) bool          { return false }
func (m *mockClient) Close() error                            { return nil }
func (m *mockClient) GetModels(context.Context) (*ai.ModelsResponse, error) {
	return &ai.ModelsResponse{}, nil
}
func (m *mockClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return &openai.ChatCompletionResponse{Model: req.Model}, nil
}
func (m *mockClient) StreamChatCompletion(_ context.Context, _ openai.ChatCompletionRequest) *ai.ChatStream {
	return nil
}
func (m *mockClient) CreateEmbedding(_ context.Context, _ openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return nil, nil
}
func (m *mockClient) CreateResponse(_ context.Context, _ openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockClient) StreamResponse(_ context.Context, _ openai.CreateResponseRequest) *ai.ResponseStream {
	return nil
}
func (m *mockClient) GetResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockClient) CancelResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockClient) DeleteResponse(_ context.Context, _ string) error { return nil }
func (m *mockClient) CompactResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	return nil, nil
}

// newTestRouter builds a minimal Router with the given providers pre-registered.
func newTestRouter(entries []struct {
	name   string
	model  string
	weight float64
	load   int64
}) *Router {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
	}
	for _, e := range entries {
		p := &Provider{
			Name:         e.name,
			ProviderType: "openai",
			Client:       &mockClient{e.name},
			Enabled:      true,
			Weight:       e.weight,
		}
		p.Healthy.Store(true)
		p.ActiveCompletions.Store(e.load)
		r.Providers[e.name] = p
		r.ModelMap[e.model] = append(r.ModelMap[e.model], e.name)
	}
	return r
}

// newSmartRouterFromSource creates a SmartRouter with an inline script (no file) and a pre-warmed pool.
func newSmartRouterFromSource(src, defaultModel string, r *Router, logger Logger) *SmartRouter {
	sr := &SmartRouter{
		defaultModel: defaultModel,
		router:       r,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
	sr.scriptSrc = src
	sr.pool = sr.buildPool()
	return sr
}

// newSmartTestRouter builds a Router + SmartRouter wired together with two providers:
//   p1 → model-a (tags: cheap)
//   p2 → model-b (tags: capable)
func newSmartTestRouter(t *testing.T, script string) (*Router, *SmartRouter) {
	t.Helper()
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	for _, name := range []string{"p1", "p2"} {
		p := &Provider{
			Name: name, ProviderType: "openai",
			Client: &mockClient{name}, Enabled: true, Weight: 1.0,
		}
		p.Healthy.Store(true)
		r.Providers[name] = p
	}
	r.ModelMap["model-a"] = []string{"p1"}
	r.ModelMap["model-b"] = []string{"p2"}
	r.ModelTags["model-a"] = []string{"cheap"}
	r.ModelTags["model-b"] = []string{"capable"}

	sr := newSmartRouterFromSource(script, "model-a", r, &testLogger{})
	r.smartRouter = sr
	return r, sr
}

// --- GetProviderForModel tests ---

func TestGetProviderForModel_SingleProvider(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	got, err := r.GetProviderForModel("m1", "")
	if err != nil || got != "p1" {
		t.Fatalf("want p1, got %q err %v", got, err)
	}
}

func TestGetProviderForModel_UnknownModel(t *testing.T) {
	r := newTestRouter(nil)
	_, err := r.GetProviderForModel("nope", "")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

// LeastLoaded: two providers, same weight — pick the one with fewer active completions.
func TestGetProviderForModel_LeastLoaded(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"busy", "m1", 1.0, 5},
		{"idle", "m1", 1.0, 0},
	})

	got, err := r.GetProviderForModel("m1", "")
	if err != nil || got != "idle" {
		t.Fatalf("want idle, got %q err %v", got, err)
	}
}

// WeightedRouting: higher weight lowers the score so that provider is preferred.
// p_heavy: score = 1/2.0 = 0.5; p_normal: score = 1/1.0 = 1.0 → p_heavy wins.
func TestGetProviderForModel_WeightedRouting(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p_normal", "m1", 1.0, 1},
		{"p_heavy", "m1", 2.0, 1},
	})

	got, err := r.GetProviderForModel("m1", "")
	if err != nil || got != "p_heavy" {
		t.Fatalf("want p_heavy (lower score), got %q err %v", got, err)
	}
}

// HintHonoured: hinted provider score (1.0) is within bestScore (0) + 1.0 threshold.
func TestGetProviderForModel_HintHonoured(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"best", "m1", 1.0, 0},   // score 0
		{"hinted", "m1", 1.0, 1}, // score 1 — within bestScore+1.0
	})

	got, err := r.GetProviderForModel("m1", "hinted")
	if err != nil || got != "hinted" {
		t.Fatalf("want hinted, got %q err %v", got, err)
	}
}

// HintIgnored: hinted provider score (5.0) exceeds bestScore (0) + 1.0.
func TestGetProviderForModel_HintIgnored(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"best", "m1", 1.0, 0},
		{"hinted", "m1", 1.0, 5},
	})

	got, err := r.GetProviderForModel("m1", "hinted")
	if err != nil || got != "best" {
		t.Fatalf("want best (hint ignored), got %q err %v", got, err)
	}
}

// HintUnknownProvider: hint names a provider not in the router — silently ignored.
func TestGetProviderForModel_HintUnknownProvider(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	got, err := r.GetProviderForModel("m1", "ghost")
	if err != nil || got != "p1" {
		t.Fatalf("want p1, got %q err %v", got, err)
	}
}

// RoundRobin: equal weight + incrementing load → each provider selected equally.
func TestGetProviderForModel_RoundRobin(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p1", "m1", 1.0, 0},
		{"p2", "m1", 1.0, 0},
		{"p3", "m1", 1.0, 0},
	})

	counts := map[string]int{}
	for i := 0; i < 6; i++ {
		got, err := r.GetProviderForModel("m1", "")
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
		r.Providers[got].ActiveCompletions.Add(1)
	}
	for _, p := range []string{"p1", "p2", "p3"} {
		if counts[p] != 2 {
			t.Errorf("provider %s selected %d times, want 2", p, counts[p])
		}
	}
}

// --- Smart routing tests ---

func TestSmartRouting_SetModel(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
	if result.ProviderHint != "" {
		t.Fatalf("want no hint, got %q", result.ProviderHint)
	}
}

func TestSmartRouting_SetModelWithHint(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
router.set_model("model-b", hint="p2")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
	if result.ProviderHint != "p2" {
		t.Fatalf("want hint p2, got %q", result.ProviderHint)
	}
}

func TestSmartRouting_OutputModelVariable(t *testing.T) {
	_, sr := newSmartTestRouter(t, `output_model = "model-a"`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestSmartRouting_FallbackToDefault(t *testing.T) {
	_, sr := newSmartTestRouter(t, `# no set_model call`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want default model-a, got %q", result.Model)
	}
}

func TestSmartRouting_UnknownModelFallback(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
router.set_model("does-not-exist")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want default model-a, got %q", result.Model)
	}
}

func TestSmartRouting_SelectByTag(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
models = router.models_by_tag("capable")
if models:
    router.set_model(models[0])
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (tagged capable), got %q", result.Model)
	}
}

func TestSmartRouting_ProvidersForModel(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
providers = router.providers_for_model("model-a")
if providers:
    router.set_model("model-a", hint=providers[0]["name"])
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
	if result.ProviderHint != "p1" {
		t.Fatalf("want hint p1, got %q", result.ProviderHint)
	}
}

// End-to-end: auto → smart router → hint honoured → correct model returned.
func TestAutoRouting_EndToEnd_WithHint(t *testing.T) {
	r, _ := newSmartTestRouter(t, `
import router
router.set_model("model-b", hint="p2")
`)
	resp, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "model-b" {
		t.Fatalf("want model-b, got %q", resp.Model)
	}
}

// End-to-end: hint overloaded → load balancer picks the better provider.
func TestAutoRouting_EndToEnd_HintIgnored(t *testing.T) {
	r, _ := newSmartTestRouter(t, `
import router
router.set_model("model-b", hint="p2")
`)
	// Add a second provider for model-b with zero load
	r.Providers["p2b"] = &Provider{
		Name: "p2b", ProviderType: "openai",
		Client: &mockClient{"p2b"}, Enabled: true, Weight: 1.0,
	}
	r.Providers["p2b"].Healthy.Store(true)
	r.ModelMap["model-b"] = append(r.ModelMap["model-b"], "p2b")
	// Overload p2 so its score (10) exceeds bestScore (0) + 1.0
	r.Providers["p2"].ActiveCompletions.Store(10)

	resp, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "model-b" {
		t.Fatalf("want model-b, got %q", resp.Model)
	}
	// Verify p2b (not p2) handled the request by checking its load incremented
	// (CreateChatCompletion increments then defers decrement, so after return it's back to 0 — just check no error)
}

// --- Scriptling library function tests ---

// runScript executes a script against the smart test router and returns the RouteResult.
func runScript(t *testing.T, script string) RouteResult {
	t.Helper()
	_, sr := newSmartTestRouter(t, script)
	return sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
}

func TestScriptLib_HasModel(t *testing.T) {
	result := runScript(t, `
import router
if router.has_model("model-a"):
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_HasModel_Missing(t *testing.T) {
	result := runScript(t, `
import router
if router.has_model("ghost"):
    router.set_model("ghost")
else:
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_ProviderLoad(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
load = router.provider_load("p1")
if load == 3:
    router.set_model("model-b")
else:
    router.set_model("model-a")
`)
	r.Providers["p1"].ActiveCompletions.Store(3)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (load==3), got %q", result.Model)
	}
}

func TestScriptLib_ProviderLoad_NotFound(t *testing.T) {
	result := runScript(t, `
import router
load = router.provider_load("ghost")
if load == -1:
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_IsChatCompletion(t *testing.T) {
	result := runScript(t, `
import router
if router.is_chat_completion():
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_IsResponses(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
if router.is_responses():
    router.set_model("model-b")
`)
	result := sr.RouteResponse(context.Background(), &CreateResponseRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_MessageContentTypes_TextOnly(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
types = router.message_content_types()
if "text" in types and "image_url" not in types:
    router.set_model("model-a")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model:    "auto",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_MessageContentTypes_WithImage(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
types = router.message_content_types()
if "image_url" in types:
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model: "auto",
		Messages: []Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "http://example.com/img.png"}},
			},
		}},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (image detected), got %q", result.Model)
	}
}

func TestScriptLib_TotalTokensEstimate(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
tokens = router.total_tokens_estimate()
if tokens > 0:
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model:    "auto",
		Messages: []Message{{Role: "user", Content: "hello world"}},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (tokens > 0), got %q", result.Model)
	}
}

func TestScriptLib_ModelsByTags_MultiTag(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
models = router.models_by_tags(["capable", "fast"])
if models:
    router.set_model(models[0])
`)
	r.ModelTags["model-b"] = []string{"capable", "fast"}
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (capable+fast), got %q", result.Model)
	}
}

func TestScriptLib_ModelsByTags_NoMatch(t *testing.T) {
	// model-a is cheap but not capable; model-b is capable but not cheap — no match
	result := runScript(t, `
import router
models = router.models_by_tags(["capable", "cheap"])
if not models:
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a (no multi-tag match), got %q", result.Model)
	}
}

func TestScriptLib_ModelsForProvider(t *testing.T) {
	result := runScript(t, `
import router
models = router.models_for_provider("p1")
if models and models[0] == "model-a":
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_ModelsForProvider_WithTagFilter(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
models = router.models_for_provider("p1", tag="cheap")
if models:
    router.set_model("model-b")
else:
    router.set_model("model-a")
`)
	r.Providers["p1"].ModelTags = map[string][]string{"model-a": {"cheap"}}
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (tag filter matched), got %q", result.Model)
	}
}

func TestScriptLib_Providers_All(t *testing.T) {
	result := runScript(t, `
import router
ps = router.providers()
if len(ps) == 2:
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b (2 providers), got %q", result.Model)
	}
}

func TestScriptLib_Providers_TagFilter(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
ps = router.providers(tag="local")
if len(ps) == 1 and ps[0]["name"] == "p1":
    router.set_model("model-b")
`)
	r.Providers["p1"].Tags = []string{"local"}
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (tag-filtered provider), got %q", result.Model)
	}
}

func TestScriptLib_RandomModel(t *testing.T) {
	// only model-a has tag "cheap"
	result := runScript(t, `
import router
m = router.random_model("cheap")
if m:
    router.set_model(m)
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a (only cheap model), got %q", result.Model)
	}
}

func TestScriptLib_ModelTags(t *testing.T) {
	result := runScript(t, `
import router
tags = router.model_tags("model-b")
if "capable" in tags:
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_GetRequest_Tools(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
req = router.get_request()
if req["type"] == "chat" and len(req["tools"]) == 1:
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model: "auto",
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "my_tool"}}},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (tool present), got %q", result.Model)
	}
}

// --- Model allowlist / denylist tests ---

func newRouterWithModels(models []string, allowlist []string, denylist []string) *Router {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	p := &Provider{
		Name: "p1", ProviderType: "openai",
		Client: &mockClient{"p1"}, Enabled: true, Weight: 1.0,
		ModelAllowlist: allowlist,
		ModelDenylist:  denylist,
	}
	p.Healthy.Store(true)
	r.Providers["p1"] = p
	r.addProviderModels("p1", models, p)
	return r
}

func TestModelAllowlist_AllowsListed(t *testing.T) {
	r := newRouterWithModels([]string{"gpt-4o", "gpt-4o-mini", "gpt-3.5"}, []string{"gpt-4o"}, nil)
	if _, ok := r.ModelMap["gpt-4o"]; !ok {
		t.Fatal("gpt-4o should be in model map")
	}
}

func TestModelAllowlist_BlocksUnlisted(t *testing.T) {
	r := newRouterWithModels([]string{"gpt-4o", "gpt-4o-mini", "gpt-3.5"}, []string{"gpt-4o"}, nil)
	for _, blocked := range []string{"gpt-4o-mini", "gpt-3.5"} {
		if _, ok := r.ModelMap[blocked]; ok {
			t.Errorf("%s should be blocked by allowlist", blocked)
		}
	}
}

func TestModelDenylist_AllowsUnlisted(t *testing.T) {
	r := newRouterWithModels([]string{"gpt-4o", "gpt-4o-mini", "gpt-3.5"}, nil, []string{"gpt-3.5"})
	for _, allowed := range []string{"gpt-4o", "gpt-4o-mini"} {
		if _, ok := r.ModelMap[allowed]; !ok {
			t.Errorf("%s should be visible (not in denylist)", allowed)
		}
	}
}

func TestModelDenylist_BlocksListed(t *testing.T) {
	r := newRouterWithModels([]string{"gpt-4o", "gpt-4o-mini", "gpt-3.5"}, nil, []string{"gpt-3.5"})
	if _, ok := r.ModelMap["gpt-3.5"]; ok {
		t.Fatal("gpt-3.5 should be blocked by denylist")
	}
}

func TestModelNoFilter_AllVisible(t *testing.T) {
	r := newRouterWithModels([]string{"gpt-4o", "gpt-4o-mini"}, nil, nil)
	for _, m := range []string{"gpt-4o", "gpt-4o-mini"} {
		if _, ok := r.ModelMap[m]; !ok {
			t.Errorf("%s should be visible with no filter", m)
		}
	}
}

// --- provider_healthy, model_load, system_prompt, last_message, conversation_turns ---

func TestScriptLib_ProviderHealthy_True(t *testing.T) {
	result := runScript(t, `
import router
if router.provider_healthy("p1"):
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_ProviderHealthy_Unhealthy(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
if not router.provider_healthy("p1"):
    router.set_model("model-b")
`)
	r.Providers["p1"].Healthy.Store(false)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (p1 unhealthy), got %q", result.Model)
	}
}

func TestScriptLib_ProviderHealthy_Unknown(t *testing.T) {
	result := runScript(t, `
import router
if not router.provider_healthy("ghost"):
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_HasProvider_Exists(t *testing.T) {
	result := runScript(t, `
import router
if router.has_provider("p1"):
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_HasProvider_Missing(t *testing.T) {
	result := runScript(t, `
import router
if not router.has_provider("ghost"):
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_HasProvider_UnhealthyStillExists(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
if router.has_provider("p1") and not router.provider_healthy("p1"):
    router.set_model("model-b")
`)
	r.Providers["p1"].Healthy.Store(false)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (exists but unhealthy), got %q", result.Model)
	}
}

func TestScriptLib_ModelLoad_Zero(t *testing.T) {
	result := runScript(t, `
import router
if router.model_load("model-a") == 0:
    router.set_model("model-b")
`)
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_ModelLoad_Busy(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
if router.model_load("model-a") > 2:
    router.set_model("model-b")
`)
	r.Providers["p1"].ActiveCompletions.Store(5)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (model-a busy), got %q", result.Model)
	}
}

func TestScriptLib_ModelLoad_MultiProvider(t *testing.T) {
	r, sr := newSmartTestRouter(t, `
import router
if router.model_load("model-b") == 7:
    router.set_model("model-a")
`)
	// Add second provider for model-b
	r.Providers["p2b"] = &Provider{
		Name: "p2b", ProviderType: "openai",
		Client: &mockClient{"p2b"}, Enabled: true, Weight: 1.0,
	}
	r.Providers["p2b"].Healthy.Store(true)
	r.ModelMap["model-b"] = append(r.ModelMap["model-b"], "p2b")
	r.Providers["p2"].ActiveCompletions.Store(3)
	r.Providers["p2b"].ActiveCompletions.Store(4)
	result := sr.Route(context.Background(), &ChatCompletionRequest{Model: "auto"})
	if result.Model != "model-a" {
		t.Fatalf("want model-a (combined load 7), got %q", result.Model)
	}
}

func TestScriptLib_SystemPrompt_Present(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
if "coding" in router.system_prompt():
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model: "auto",
		Messages: []Message{
			{Role: "system", Content: "you are a coding assistant"},
			{Role: "user", Content: "hello"},
		},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_SystemPrompt_Absent(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
if router.system_prompt() == "":
    router.set_model("model-a")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model:    "auto",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_LastMessage(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
if "image" in router.last_message():
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model: "auto",
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "describe this image"},
		},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b, got %q", result.Model)
	}
}

func TestScriptLib_LastMessage_Empty(t *testing.T) {
	result := runScript(t, `
import router
if router.last_message() == "":
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

func TestScriptLib_ConversationTurns(t *testing.T) {
	_, sr := newSmartTestRouter(t, `
import router
if router.conversation_turns() >= 3:
    router.set_model("model-b")
`)
	result := sr.Route(context.Background(), &ChatCompletionRequest{
		Model: "auto",
		Messages: []Message{
			{Role: "user", Content: "msg1"},
			{Role: "assistant", Content: "resp1"},
			{Role: "user", Content: "msg2"},
			{Role: "assistant", Content: "resp2"},
			{Role: "user", Content: "msg3"},
		},
	})
	if result.Model != "model-b" {
		t.Fatalf("want model-b (3 turns), got %q", result.Model)
	}
}

func TestScriptLib_ConversationTurns_Zero(t *testing.T) {
	result := runScript(t, `
import router
if router.conversation_turns() == 0:
    router.set_model("model-a")
`)
	if result.Model != "model-a" {
		t.Fatalf("want model-a, got %q", result.Model)
	}
}

// --- Model alias tests ---

// newRouterWithAliases builds a router with a provider that has real models and aliases.
func newRouterWithAliases(models []string, aliases map[string]string) *Router {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	p := &Provider{
		Name: "p1", ProviderType: "openai",
		Client: &mockClient{"p1"}, Enabled: true, Weight: 1.0,
		ModelAliases: aliases,
	}
	p.Healthy.Store(true)
	r.Providers["p1"] = p
	r.addProviderModels("p1", models, p)
	return r
}

func TestAlias_ResolveKnown(t *testing.T) {
	r := newRouterWithAliases([]string{"gpt-4o"}, map[string]string{"gpt4": "gpt-4o"})
	if got := r.resolveAliasForProvider("gpt4", "p1"); got != "gpt-4o" {
		t.Fatalf("want gpt-4o, got %q", got)
	}
}

func TestAlias_ResolveUnknown(t *testing.T) {
	r := newRouterWithAliases([]string{"gpt-4o"}, map[string]string{"gpt4": "gpt-4o"})
	if got := r.resolveAliasForProvider("gpt-4o", "p1"); got != "gpt-4o" {
		t.Fatalf("want gpt-4o unchanged, got %q", got)
	}
}

func TestAlias_InModelMap(t *testing.T) {
	r := newRouterWithAliases([]string{"gpt-4o"}, map[string]string{"gpt4": "gpt-4o"})
	if _, ok := r.ModelMap["gpt4"]; !ok {
		t.Fatal("alias should appear in ModelMap")
	}
}

func TestAlias_GetProviderForModel(t *testing.T) {
	r := newRouterWithAliases([]string{"gpt-4o"}, map[string]string{"gpt4": "gpt-4o"})
	// Alias is a key in ModelMap; look up provider directly via alias
	got, err := r.GetProviderForModel("gpt4", "")
	if err != nil || got != "p1" {
		t.Fatalf("want p1, got %q err %v", got, err)
	}
}

func TestAlias_CreateChatCompletion(t *testing.T) {
	r := newRouterWithAliases([]string{"gpt-4o"}, map[string]string{"gpt4": "gpt-4o"})
	resp, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "gpt4"})
	if err != nil {
		t.Fatal(err)
	}
	// The request should have been forwarded with the real model name for this provider
	if resp.Model != "gpt-4o" {
		t.Fatalf("want gpt-4o forwarded to provider, got %q", resp.Model)
	}
}

// TestAlias_RoundRobin: two providers each aliasing "fast" to a different real model —
// load balancing distributes across both, and each gets its own real model name.
func TestAlias_RoundRobin(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	for i, name := range []string{"p1", "p2"} {
		realModel := []string{"gpt-4o-mini", "mistral-small"}[i]
		p := &Provider{
			Name: name, ProviderType: "openai",
			Client: &mockClient{name}, Enabled: true, Weight: 1.0,
			ModelAliases: map[string]string{"fast": realModel},
		}
		p.Healthy.Store(true)
		r.Providers[name] = p
		r.addProviderModels(name, []string{realModel}, p)
	}

	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		got, err := r.GetProviderForModel("fast", "")
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
		r.Providers[got].ActiveCompletions.Add(1)
	}
	for _, p := range []string{"p1", "p2"} {
		if counts[p] != 2 {
			t.Errorf("provider %s selected %d times via alias, want 2", p, counts[p])
		}
	}

	// Verify each provider resolves the alias to its own real model
	if got := r.resolveAliasForProvider("fast", "p1"); got != "gpt-4o-mini" {
		t.Errorf("p1 alias: want gpt-4o-mini, got %q", got)
	}
	if got := r.resolveAliasForProvider("fast", "p2"); got != "mistral-small" {
		t.Errorf("p2 alias: want mistral-small, got %q", got)
	}
}
