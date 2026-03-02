package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	if r.Providers["p1"].Healthy {
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

	if !r.Providers["p1"].Healthy {
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
	r.Providers["sick"].Healthy = false

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
	r.Providers["p1"].Healthy = false
	r.Providers["p2"].Healthy = false

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
		r.Providers[name] = &Provider{Name: name, Enabled: true, Healthy: true, Weight: 1.0}
	}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("p%d", i+1)
		models := []string{"shared-model", fmt.Sprintf("model-%d", i+1)}
		wg.Add(1)
		go func(n string, m []string) {
			defer wg.Done()
			r.addProviderModels(n, m, r.Providers[n])
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
		Enabled: true, Healthy: true, Weight: 1.0,
	}
	r.ModelMap["m1"] = []string{"p1"}

	_, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if r.Providers["p1"].Healthy {
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
		Enabled: true, Healthy: true, Weight: 1.0,
	}
	r.ModelMap["m1"] = []string{"p1"}

	_, err := r.CreateChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !r.Providers["p1"].Healthy {
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
			Client: &countingClient{name: pName, counter: c},
			Enabled: true, Healthy: true, Weight: 1.0,
		}
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

type countingClient struct {
	mockClient
	name    string
	counter *atomic.Int64
}

func (c *countingClient) ChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	c.counter.Add(1)
	return &openai.ChatCompletionResponse{Model: req.Model}, nil
}

// --- checkDisabledProviders skips static-model providers ---

func TestCheckDisabledProviders_SkipsStaticModelProviders(t *testing.T) {
	r := &Router{
		Providers:    make(map[string]*Provider),
		ModelMap:     make(map[string][]string),
		ModelTags:    make(map[string][]string),
		logger:       &testLogger{},
		shutdownChan: make(chan struct{}),
	}
	// Static model provider (has Models set) — should be skipped by health check
	r.Providers["static"] = &Provider{
		Name: "static", Enabled: true, Healthy: false,
		Models: []string{"m1"},
		Client: &mockClient{"static"},
		Weight: 1.0,
	}

	// Run checkDisabledProviders — static provider should NOT be re-enabled
	r.checkDisabledProviders()

	if r.Providers["static"].Healthy {
		t.Fatal("static model provider should not be re-enabled by health check")
	}
}
