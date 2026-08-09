package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai/openai"
)

// testLogger is declared in mcp_server_test.go

// --- Provider disable/enable lifecycle ---

func TestDisableProvider_MarksUnhealthy(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})
	r.logger = &testLogger{}

	r.DisableProvider("p1", "test")

	if r.Providers["p1"].Healthy.Load() {
		t.Fatal("provider should be unhealthy after disable")
	}
}

func TestDisableProvider_RemovesModels(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})
	r.logger = &testLogger{}

	r.DisableProvider("p1", "test")

	if _, ok := r.ModelMap["m1"]; ok {
		t.Fatal("model should be removed when sole provider is disabled")
	}
}

func TestDisableProvider_KeepsModelForOtherProvider(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p1", "m1", 1.0, 0},
		{"p2", "m1", 1.0, 0},
	})
	r.logger = &testLogger{}

	r.DisableProvider("p1", "test")

	providers, ok := r.ModelMap["m1"]
	if !ok {
		t.Fatal("model should remain when another provider still serves it")
	}
	for _, p := range providers {
		if p == "p1" {
			t.Fatal("disabled provider should not remain in model map")
		}
	}
}

func TestDisableProvider_Idempotent(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})
	r.logger = &testLogger{}

	r.DisableProvider("p1", "first")
	r.DisableProvider("p1", "second") // should not panic or double-remove
}

// removeProviderModels is what reloadProviders uses when a provider is
// disabled/deleted via the admin UI. It must drop the provider's name from
// every model entry and delete entries left with no providers — otherwise a
// disabled provider keeps showing up on the models page.
func TestRemoveProviderModels_ClearsEntries(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p1", "m1", 1.0, 0},
		{"p1", "shared", 1.0, 0},
		{"p2", "shared", 1.0, 0},
		{"p2", "m2", 1.0, 0},
	})
	r.logger = &testLogger{}

	r.removeProviderModels("p1")

	if _, ok := r.ModelMap["m1"]; ok {
		t.Fatal("model served only by p1 should be removed")
	}
	providers, ok := r.ModelMap["shared"]
	if !ok {
		t.Fatal("shared model should remain (p2 still serves it)")
	}
	for _, p := range providers {
		if p == "p1" {
			t.Fatal("p1 should be removed from shared model entry")
		}
	}
	if _, ok := r.ModelMap["m2"]; !ok {
		t.Fatal("unrelated model m2 should be untouched")
	}
}

func TestEnableProvider_MarksHealthy(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})
	r.logger = &testLogger{}

	r.DisableProvider("p1", "test")
	r.EnableProvider("p1")

	if !r.Providers["p1"].Healthy.Load() {
		t.Fatal("provider should be healthy after enable")
	}
}

func TestGetProviderForModel_SkipsUnhealthy(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"sick", "m1", 1.0, 0},
		{"well", "m1", 1.0, 0},
	})
	r.logger = &testLogger{}
	r.Providers["sick"].Healthy.Store(false)

	got, err := r.GetProviderForModel("m1", "")
	if err != nil || got != "well" {
		t.Fatalf("want well, got %q err %v", got, err)
	}
}

// TestGetProviderForModel_AllUnhealthy: two providers both unhealthy → error.
func TestGetProviderForModel_AllUnhealthy(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p1", "m1", 1.0, 0},
		{"p2", "m1", 1.0, 0},
	})
	r.logger = &testLogger{}
	r.Providers["p1"].Healthy.Store(false)
	r.Providers["p2"].Healthy.Store(false)

	_, err := r.GetProviderForModel("m1", "")
	if err == nil {
		t.Fatal("expected error when all providers are unhealthy")
	}
}

// --- Active completion counter ---

func TestActiveCompletions_IncrementDecrement(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	r.incrementActiveCompletions("p1")
	if r.Providers["p1"].ActiveCompletions.Load() != 1 {
		t.Fatal("expected 1 active completion after increment")
	}
	r.decrementActiveCompletions("p1")
	if r.Providers["p1"].ActiveCompletions.Load() != 0 {
		t.Fatal("expected 0 active completions after decrement")
	}
}

func TestActiveCompletions_ParallelRequests(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})
	r.logger = &testLogger{}

	const n = 50
	var wg sync.WaitGroup
	var peak atomic.Int64

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			r.incrementActiveCompletions("p1")
			cur := r.Providers["p1"].ActiveCompletions.Load()
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			r.decrementActiveCompletions("p1")
		}()
	}
	wg.Wait()

	if r.Providers["p1"].ActiveCompletions.Load() != 0 {
		t.Fatal("active completions should be 0 after all goroutines finish")
	}
	if peak.Load() == 0 {
		t.Fatal("peak should be > 0 during concurrent requests")
	}
}

// --- isConnectionError ---

func TestIsConnectionError_Patterns(t *testing.T) {
	r := &Router{}
	cases := []struct {
		err    string
		expect bool
	}{
		{"connection refused", true},
		{"connection reset by peer", true},
		{"connection timeout", true},
		{"no such host", true},
		{"network is unreachable", true},
		{"dial tcp: connect: connection refused", true},
		{"EOF", true},
		{"connection closed", true},
		// Should NOT trigger disable
		{"timeout awaiting response headers", false},
		{"server returned status 500", false},
		{"Model does not exist", false},
		{"context deadline exceeded", false},
	}
	for _, c := range cases {
		got := r.isConnectionError(errors.New(c.err))
		if got != c.expect {
			t.Errorf("isConnectionError(%q) = %v, want %v", c.err, got, c.expect)
		}
	}
}

func TestIsConnectionError_Nil(t *testing.T) {
	r := &Router{}
	if r.isConnectionError(nil) {
		t.Fatal("nil error should not be a connection error")
	}
}

// --- Concurrent model map access ---

func TestModelMap_ConcurrentReadWrite(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{
		{"p1", "m1", 1.0, 0},
		{"p2", "m1", 1.0, 0},
	})
	r.logger = &testLogger{}

	var wg sync.WaitGroup
	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.GetProviderForModel("m1", "") //nolint
		}()
	}
	// Concurrent disable/enable
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.DisableProvider("p1", "concurrent test")
			r.EnableProvider("p1")
		}()
	}
	wg.Wait()
}

func TestAddProviderModels_ConcurrentRefresh(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	for _, name := range []string{"p1", "p2", "p3"} {
		p := &Provider{Name: name, Enabled: true, Weight: 1.0}
		p.Healthy.Store(true)
		r.Providers[name] = p
	}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("p%d", i+1)
		models := []string{"shared-model", fmt.Sprintf("model-%d", i+1)}
		wg.Add(1)
		go func(n string, m []string) {
			defer wg.Done()
			r.addProviderModels(n, m, r.Providers[n], nil)
		}(name, models)
	}
	wg.Wait()

	// shared-model should have at least one provider
	if _, ok := r.ModelMap["shared-model"]; !ok {
		t.Fatal("shared-model should be in model map after concurrent refresh")
	}
}

// --- CreateChatCompletion disables provider on connection error ---

type errorClient struct {
	mockClient
	err error
}

func (e *errorClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return nil, e.err
}

func TestCreateChatCompletion_DisablesOnConnectionError(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	r.Providers["p1"] = &Provider{
		Name: "p1", ProviderType: "openai",
		Client:  &errorClient{err: errors.New("connection refused")},
		Enabled: true, Weight: 1.0,
	}
	r.Providers["p1"].Healthy.Store(true)
	r.ModelMap["m1"] = []string{"p1"}

	_, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if r.Providers["p1"].Healthy.Load() {
		t.Fatal("provider should be disabled after connection error")
	}
}

func TestCreateChatCompletion_DoesNotDisableOnAPIError(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	r.Providers["p1"] = &Provider{
		Name: "p1", ProviderType: "openai",
		Client:  &errorClient{err: errors.New("server returned status 500: Model does not exist")},
		Enabled: true, Weight: 1.0,
	}
	r.Providers["p1"].Healthy.Store(true)
	r.ModelMap["m1"] = []string{"p1"}

	_, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !r.Providers["p1"].Healthy.Load() {
		t.Fatal("provider should remain healthy after non-connection API error")
	}
}

// --- HandleMessages streaming: auto routing ---

func TestHandleMessages_StreamAutoRouting(t *testing.T) {
	// Build a router with smart routing that picks model-b
	r, _ := newSmartTestRouter(t, `
import router
router.set_model("model-b", hint="p2")
`)
	r.config = &types.Config{}

	// Replace p2's client with one that records the model
	var gotModel string
	r.Providers["p2"].Client = &recordingClient{onStream: func(model string) {
		gotModel = model
	}}

	reqBody := map[string]any{
		"model":  "auto",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleMessages(rec, req)

	if gotModel != "model-b" {
		t.Fatalf("want model-b routed via auto, got %q", gotModel)
	}
}

// recordingClient records which model was streamed.
type recordingClient struct {
	mockClient
	onStream func(model string)
}

func (rc *recordingClient) StreamChatCompletion(_ context.Context, req openai.ChatCompletionRequest) *openai.ChatStream {
	if rc.onStream != nil {
		rc.onStream(req.Model)
	}
	// Return an empty stream
	ch := make(chan openai.ChatCompletionResponse)
	errCh := make(chan error, 1)
	close(ch)
	close(errCh)
	return openai.NewChatStream(context.Background(), ch, errCh)
}

// --- HandleMessages streaming: active completion counter ---

func TestHandleMessages_StreamDecrementsCounter(t *testing.T) {
	r, _ := newSmartTestRouter(t, `import router; router.set_model("model-a")`)
	r.config = &types.Config{}

	// Replace p1's client with one that returns a real (empty) stream
	r.Providers["p1"].Client = &recordingClient{}

	reqBody := map[string]any{
		"model":  "model-a",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleMessages(rec, req)

	if r.Providers["p1"].ActiveCompletions.Load() != 0 {
		t.Fatalf("active completions should be 0 after stream, got %d",
			r.Providers["p1"].ActiveCompletions.Load())
	}
}

// --- HandleChatCompletions: model not found returns 404 ---

func TestHandleChatCompletions_ModelNotFound(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
		config:    &types.Config{},
	}
	r.mux = http.NewServeMux()

	reqBody := map[string]any{
		"model":    "ghost-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// --- Parallel CreateChatCompletion: load balancing under concurrency ---

func TestCreateChatCompletion_ParallelLoadBalancing(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	counts := make(map[string]*atomic.Int64)
	for _, name := range []string{"p1", "p2", "p3"} {
		c := &atomic.Int64{}
		counts[name] = c
		pName := name
		r.Providers[name] = &Provider{
			Name: name, ProviderType: "openai",
			Client:  &countingClient{name: pName, counter: c, latency: 2 * time.Millisecond},
			Enabled: true, Weight: 1.0,
		}
		r.Providers[name].Healthy.Store(true)
		r.ModelMap["m1"] = append(r.ModelMap["m1"], name)
	}

	const n = 30
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"}) //nolint
		}()
	}
	wg.Wait()

	total := int64(0)
	for _, c := range counts {
		total += c.Load()
	}
	if total != n {
		t.Fatalf("want %d total completions, got %d", n, total)
	}
	for name, c := range counts {
		if c.Load() == 0 {
			t.Errorf("provider %s handled 0 requests — load balancing not working", name)
		}
	}
}

// TestLoadBalancing_SequentialPinsToWarmProvider: under low-concurrency
// (sequential) traffic to a single model, requests pin to the first provider
// that warms the model (model-match tiebreak), rather than spreading. Load
// expands to other providers only when the warm one is busy — see the burst test.
func TestLoadBalancing_SequentialPinsToWarmProvider(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	counts := make(map[string]*atomic.Int64)
	for _, name := range []string{"p1", "p2", "p3"} {
		c := &atomic.Int64{}
		counts[name] = c
		pName := name
		r.Providers[name] = &Provider{
			Name: name, ProviderType: "openai",
			Client:  &countingClient{name: pName, counter: c},
			Enabled: true, Weight: 1.0,
		}
		r.Providers[name].Healthy.Store(true)
		r.ModelMap["m1"] = append(r.ModelMap["m1"], name)
	}

	const n = 30
	for i := 0; i < n; i++ {
		r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"}) //nolint
	}

	// Exactly one provider handled all traffic (the one that warmed m1 first);
	// the other two stayed idle — pinning, not spreading, for sequential traffic.
	var used []string
	for _, name := range []string{"p1", "p2", "p3"} {
		if counts[name].Load() > 0 {
			used = append(used, name)
		}
	}
	if len(used) != 1 {
		t.Errorf("expected exactly one provider to handle sequential traffic (pinning), got %v (%v)", used, counts)
	}
}

// --- selectFromTies unit tests (deterministic; set LastServed state directly) ---

func providersWithLastServed(entries map[string]*lastServed) map[string]*Provider {
	m := make(map[string]*Provider, len(entries))
	for name, ls := range entries {
		p := &Provider{Name: name, Enabled: true, Weight: 1.0}
		p.Healthy.Store(true)
		if ls != nil {
			p.LastServed.Store(ls)
		}
		m[name] = p
	}
	return m
}

// LRU round-robins among multiple warm providers: pick the one idle longest,
// so sequential traffic alternates instead of piling on one.
func TestSelectFromTies_LRURoundRobinsWarmProviders(t *testing.T) {
	providers := providersWithLastServed(map[string]*lastServed{
		"a": {model: "m1", at: 100},
		"b": {model: "m1", at: 200},
	})

	// Both warm for m1; a is idle longer (at=100) → a wins.
	if got := selectFromTies([]string{"a", "b"}, "m1", providers); got != "a" {
		t.Fatalf("first pick = %q, want a (idle longest)", got)
	}
	// a just served → now newest; b becomes idle-longest → b wins next.
	providers["a"].LastServed.Store(&lastServed{model: "m1", at: 300})
	if got := selectFromTies([]string{"a", "b"}, "m1", providers); got != "b" {
		t.Fatalf("second pick = %q, want b (now idle longest)", got)
	}
}

// Model-match avoids a forced reload: a provider serving a different model is
// skipped in favour of one that already has the requested model.
func TestSelectFromTies_ModelMatchAvoidsReload(t *testing.T) {
	providers := providersWithLastServed(map[string]*lastServed{
		"has_m1": {model: "m1", at: 500}, // recently busy with m1
		"has_m2": {model: "m2", at: 0},   // idle longest, but cold for m1
	})
	// Request m1: only has_m1 matches → picked, even though has_m2 is idle longer.
	if got := selectFromTies([]string{"has_m1", "has_m2"}, "m1", providers); got != "has_m1" {
		t.Fatalf("pick = %q, want has_m1 (only m1 match; avoid reload on has_m2)", got)
	}
}

// Cold LRU warms a fresh provider first: when no candidate has the model, the
// never-used provider (at=0) wins over a reused hand, spreading first loads.
func TestSelectFromTies_ColdLRUWarmsFreshProvider(t *testing.T) {
	providers := providersWithLastServed(map[string]*lastServed{
		"reused": {model: "m1", at: 1000},
		"fresh":  nil, // never served → at=0
	})
	// Request m2 (neither has it): no match → LRU → fresh (at=0 < 1000).
	if got := selectFromTies([]string{"reused", "fresh"}, "m2", providers); got != "fresh" {
		t.Fatalf("pick = %q, want fresh (cold LRU warms it first)", got)
	}
}

// Multi-model distribution with NO forced reloads: with ≥1 provider per model
// already warm, each model stays on its own provider — no provider serves two
// different models (which would imply an eviction/reload).
func TestSelectFromTies_MultiModelNoReload(t *testing.T) {
	t.Run("two models", func(t *testing.T) {
		providers := providersWithLastServed(map[string]*lastServed{
			"a": {model: "m1", at: 100},
			"b": {model: "m2", at: 100},
			"c": nil,
		})
		all := []string{"a", "b", "c"}
		for i := 0; i < 20; i++ {
			if got := selectFromTies(all, "m1", providers); got != "a" {
				t.Errorf("m1 pick %d = %q, want a", i, got)
			}
			if got := selectFromTies(all, "m2", providers); got != "b" {
				t.Errorf("m2 pick %d = %q, want b", i, got)
			}
		}
		// c is never picked: no reload forced onto it while a/b are warm.
		if providers["c"].LastServed.Load() != nil {
			t.Errorf("c was wrongly assigned a model (forced reload)")
		}
	})

	t.Run("three models", func(t *testing.T) {
		providers := providersWithLastServed(map[string]*lastServed{
			"a": {model: "m1", at: 100},
			"b": {model: "m2", at: 100},
			"c": {model: "m3", at: 100},
		})
		all := []string{"a", "b", "c"}
		for _, tc := range []struct{ model, want string }{
			{"m1", "a"}, {"m2", "b"}, {"m3", "c"},
			{"m1", "a"}, {"m3", "c"}, {"m2", "b"},
		} {
			if got := selectFromTies(all, tc.model, providers); got != tc.want {
				t.Errorf("model %s → %q, want %s", tc.model, got, tc.want)
			}
		}
	})
}

// Multi-model burst distributes across providers (each model warms a distinct
// provider), verified end-to-end through CreateChatCompletion. After the run no
// provider should have served more than one distinct model (no forced reloads),
// and every provider should be used (real distribution).
func TestLoadBalancing_MultiModelBurstDistributesWithoutReloads(t *testing.T) {
	r := &Router{
		Providers: make(map[string]*Provider),
		ModelMap:  make(map[string][]string),
		ModelTags: make(map[string][]string),
		logger:    &testLogger{},
	}
	// modelRecorder records which models each provider served.
	type rec struct {
		models map[string]bool
	}
	recs := make(map[string]*rec, 3)
	mu := sync.Mutex{}
	for _, name := range []string{"p1", "p2", "p3"} {
		recs[name] = &rec{models: map[string]bool{}}
		pName := name
		r.Providers[name] = &Provider{
			Name: name, ProviderType: "openai",
			Client: &modelRecorderClient{
				name: pName,
				record: func(model string) {
					mu.Lock()
					recs[pName].models[model] = true
					mu.Unlock()
				},
			},
			Enabled: true, Weight: 1.0,
		}
		r.Providers[name].Healthy.Store(true)
		for _, m := range []string{"m1", "m2", "m3"} {
			r.ModelMap[m] = append(r.ModelMap[m], name)
		}
	}

	// Interleave requests across 3 models so each warms a distinct provider.
	// Sequential m1→p_a, m2→p_b, m3→p_c; subsequent same-model requests pin.
	order := []string{"m1", "m2", "m3", "m1", "m2", "m3", "m1", "m2", "m3"}
	for _, m := range order {
		r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: m}) //nolint
	}

	// Each provider served exactly one distinct model (its warm model), and all
	// three providers were used — distribution without forced reloads.
	used := 0
	for _, name := range []string{"p1", "p2", "p3"} {
		n := len(recs[name].models)
		if n > 1 {
			t.Errorf("provider %s served %d models %v — forced reload", name, n, recs[name].models)
		}
		if n == 1 {
			used++
		}
	}
	if used != 3 {
		t.Errorf("expected all 3 providers used, got %d", used)
	}
}

// modelRecorderClient is a no-op Client that records the model each call served.
type modelRecorderClient struct {
	mockClient
	name   string
	record func(model string)
}

func (m *modelRecorderClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	if m.record != nil {
		m.record(req.Model)
	}
	return &openai.ChatCompletionResponse{Model: req.Model}, nil
}
type countingClient struct {
	mockClient
	name    string
	counter *atomic.Int64
	latency time.Duration // simulated per-request in-flight time (0 = instant)
}

func (c *countingClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	c.counter.Add(1)
	if c.latency > 0 {
		time.Sleep(c.latency)
	}
	return &openai.ChatCompletionResponse{Model: req.Model}, nil
}

// --- checkDisabledProviders recovers all providers ---

func TestCheckDisabledProviders_RecoversStaticModelProviders(t *testing.T) {
	r := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		ModelTags:    make(map[string][]string),
		logger:       &testLogger{},
		shutdownChan: make(chan struct{}),
	}
	// Static model provider (has Models set) — should still be recovered
	r.Providers["static"] = &Provider{
		Name: "static", Enabled: true,
		Models: []string{"m1"},
		Client: &mockClient{"static"},
		Weight: 1.0,
	}

	// Run checkDisabledProviders — static provider SHOULD be re-enabled
	r.checkDisabledProviders()

	if !r.Providers["static"].Healthy.Load() {
		t.Fatal("static model provider should be re-enabled by health check when recovered")
	}
}

func TestCheckDisabledProviders_RecoversDynamicModelProviders(t *testing.T) {
	r := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		ModelTags:    make(map[string][]string),
		logger:       &testLogger{},
		shutdownChan: make(chan struct{}),
	}
	// Dynamic model provider (no Models set) — should be recovered
	r.Providers["dynamic"] = &Provider{
		Name: "dynamic", Enabled: true,
		Models: nil, // dynamic discovery
		Client: &mockClient{"dynamic"},
		Weight: 1.0,
	}

	r.checkDisabledProviders()

	if !r.Providers["dynamic"].Healthy.Load() {
		t.Fatal("dynamic model provider should be re-enabled by health check when recovered")
	}
}

// errorMockClient always returns an error from GetModels
type errorMockClient struct {
	mockClient
}

func (c *errorMockClient) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	return nil, errors.New("connection refused")
}

func TestCheckDisabledProviders_RemainsUnhealthyOnError(t *testing.T) {
	r := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		ModelTags:    make(map[string][]string),
		logger:       &testLogger{},
		shutdownChan: make(chan struct{}),
	}
	// Provider that will fail health check
	r.Providers["failing"] = &Provider{
		Name: "failing", Enabled: true,
		Models: nil,
		Client: &errorMockClient{},
		Weight: 1.0,
	}

	r.checkDisabledProviders()

	if r.Providers["failing"].Healthy.Load() {
		t.Fatal("provider should remain unhealthy when health check fails")
	}
}

// --- Streaming error handling: upstream error before any data ---

// streamErrorClient returns a stream that errors immediately without sending any chunks.
type streamErrorClient struct {
	mockClient
	err error
}

func (c *streamErrorClient) StreamChatCompletion(_ context.Context, _ openai.ChatCompletionRequest) *openai.ChatStream {
	// Don't close the response channel — leave it open (blocking) so that
	// Next() is forced to read from the error channel. This matches real
	// usage where the error is buffered before channels are closed.
	ch := make(chan openai.ChatCompletionResponse)
	errCh := make(chan error, 1)
	errCh <- c.err
	return openai.NewChatStream(context.Background(), ch, errCh)
}

func TestHandleChatCompletions_StreamUpstreamError(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	upstreamErr := &openai.APIError{
		StatusCode: http.StatusBadRequest,
		Type:       "invalid_request_error",
		Message:    "model does not support tool calls",
	}
	r.Providers["p1"].Client = &streamErrorClient{err: upstreamErr}

	reqBody := map[string]any{
		"model":  "m1",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleChatCompletions(rec, req)

	// Should get the upstream status code, NOT 200 with bare [DONE].
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (upstream status), got %d body=%q", rec.Code, rec.Body.String())
	}

	// Response must be JSON with an error object — not an empty SSE stream.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response should be valid JSON, got: %q err=%v", rec.Body.String(), err)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("response should have error object, got: %v", resp)
	}
	if errObj["message"] != "model does not support tool calls" {
		t.Fatalf("unexpected error message: %v", errObj["message"])
	}
}

// --- Streaming error handling: upstream error mid-stream ---

// streamThenErrorClient sends one chunk then errors.
type streamThenErrorClient struct {
	mockClient
	chunk openai.ChatCompletionResponse
	err   error
}

func (c *streamThenErrorClient) StreamChatCompletion(ctx context.Context, _ openai.ChatCompletionRequest) *openai.ChatStream {
	ch := make(chan openai.ChatCompletionResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		ch <- c.chunk
		// Don't close ch — leave it open so the second Next() call blocks
		// on responseChan and is forced to read from errorChan instead.
		errCh <- c.err
	}()
	return openai.NewChatStream(ctx, ch, errCh)
}

func TestHandleChatCompletions_StreamMidStreamError(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	chunk := openai.ChatCompletionResponse{
		ID: "chatcmpl-test", Object: "chat.completion.chunk", Model: "m1",
		Choices: []openai.Choice{{
			Index:        0,
			Delta:        openai.Delta{Content: "hello"},
			FinishReason: "",
		}},
	}
	r.Providers["p1"].Client = &streamThenErrorClient{
		chunk: chunk,
		err:   errors.New("connection reset"),
	}

	reqBody := map[string]any{
		"model":  "m1",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleChatCompletions(rec, req)

	// Headers were already committed when the first chunk arrived — status
	// must be 200 (SSE), but the body must contain an error event.
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (headers committed), got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hello"`) {
		t.Fatalf("body should contain the first chunk: %q", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("body should contain an error event after mid-stream failure: %q", body)
	}
	if !strings.Contains(body, "connection reset") {
		t.Fatalf("body should contain the error message: %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("body should end with [DONE]: %q", body)
	}
}

// --- Messages (Claude) streaming: upstream error before any data ---

func TestHandleMessages_StreamUpstreamError(t *testing.T) {
	r := newTestRouter([]struct {
		name   string
		model  string
		weight float64
		load   int64
	}{{"p1", "m1", 1.0, 0}})

	upstreamErr := &openai.APIError{
		StatusCode: http.StatusBadRequest,
		Type:       "invalid_request_error",
		Message:    "bad request",
	}
	r.Providers["p1"].Client = &streamErrorClient{err: upstreamErr}

	reqBody := map[string]any{
		"model":  "m1",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	}
	data, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	r.HandleMessages(rec, req)

	// Should get the upstream status code, not 200 with empty body.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (upstream status), got %d body=%q", rec.Code, rec.Body.String())
	}

	// Response must be JSON with an error object.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response should be valid JSON, got: %q err=%v", rec.Body.String(), err)
	}
	if _, ok := resp["error"].(map[string]any); !ok {
		t.Fatalf("response should have error object, got: %v", resp)
	}
}
