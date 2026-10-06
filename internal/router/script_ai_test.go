package router

import (
	"context"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
)

// TestScriptAIDecideInProcess proves router.ai()'s client reaches the router's
// own /v1/systemone through the mux (including auth) with no network.
func TestScriptAIDecideInProcess(t *testing.T) {
	r, err := NewRouter(&types.Config{
		Server: types.ServerConfig{Host: "127.0.0.1", Port: 0, Token: "tok"},
	}, &testLogger{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	dec := &decisionClient{}
	p := &Provider{Name: "dec", ProviderType: "ollama", Client: dec, Enabled: true, Weight: 1, Models: []string{"m"}}
	p.Healthy.Store(true)
	r.Providers["dec"] = p
	r.ModelMap["m"] = []string{"dec"}

	client, err := r.scriptAI()
	if err != nil {
		t.Fatalf("scriptAI: %v", err)
	}
	dc, ok := client.(ai.DecisionCaller)
	if !ok {
		t.Fatal("script client does not support decisions")
	}
	resp, err := dc.Decide(context.Background(), ai.SystemOneRequest{
		Model:     "m",
		State:     "x",
		Questions: map[string]ai.SystemOneQuestion{"q": {Type: "choice", Instructions: "i"}},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if resp.Answers["q"].Choice != "a" || dec.last.Model != "m" {
		t.Fatalf("unexpected result: %+v / %+v", resp, dec.last)
	}
}

func TestSmartRouterSkipsScriptWhenCalledFromScript(t *testing.T) {
	sr := &SmartRouter{name: "n", defaultModel: "dflt", scriptSrc: "x = 1", logger: &testLogger{}}
	ctx := context.WithValue(context.Background(), inRoutingScriptKey{}, true)
	if got := sr.Route(ctx, &ChatCompletionRequest{}); got.Model != "dflt" {
		t.Fatalf("model = %q, want default", got.Model)
	}
}
