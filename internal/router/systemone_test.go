package router

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/mcp/ai"
)

type decisionClient struct {
	mockProviderClient
	last ai.SystemOneRequest
}

func (c *decisionClient) Decide(ctx context.Context, req ai.SystemOneRequest) (*ai.SystemOneResponse, error) {
	c.last = req
	return &ai.SystemOneResponse{Model: req.Model, Answers: map[string]ai.SystemOneAnswer{"q": {Type: "choice", Choice: "a"}}}, nil
}

func newDecisionRouter(plain, dec ai.Client) *Router {
	r := newOllamaHandlerTestRouter(plain)
	for _, p := range r.Providers {
		p.Models = []string{"m"}
	}
	r.ModelMap["m"] = []string{"mock-provider"}
	if dec != nil {
		p := &Provider{Name: "dec", ProviderType: "ollama", Client: dec, Enabled: true, Weight: 1, Models: []string{"m"}}
		p.Healthy.Store(true)
		r.Providers["dec"] = p
		r.ModelMap["m"] = append(r.ModelMap["m"], "dec")
	}
	return r
}

func postSystemOne(r *Router, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.HandleSystemOne(rec, httptest.NewRequest(http.MethodPost, "/v1/systemone", bytes.NewBufferString(body)))
	return rec
}

func TestSystemOneRoutesToDecisionCapableProvider(t *testing.T) {
	dec := &decisionClient{}
	r := newDecisionRouter(&mockProviderClient{}, dec)
	rec := postSystemOne(r, `{"model":"m","state":"x","questions":{"q":{"type":"choice","instructions":"i","criteria":{"a":null,"b":null}}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if dec.last.Model != "m" || len(dec.last.Questions) != 1 {
		t.Fatalf("request not forwarded: %+v", dec.last)
	}
}

func TestSystemOneNoCapableProvider(t *testing.T) {
	r := newDecisionRouter(&mockProviderClient{}, nil)
	rec := postSystemOne(r, `{"model":"m","state":"x","questions":{}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "mock-provider (openai)") || !strings.Contains(body, "ollama") {
		t.Fatalf("error should name the providers and the fix: %s", body)
	}
	if rec := postSystemOne(r, `{"model":"nope","state":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model status %d, want 404", rec.Code)
	}
	if rec := postSystemOne(r, `{"state":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing model status %d, want 400", rec.Code)
	}
}

type rejectingDecisionClient struct{ decisionClient }

func (c *rejectingDecisionClient) Decide(ctx context.Context, req ai.SystemOneRequest) (*ai.SystemOneResponse, error) {
	return nil, errors.New(`ollama decide failed: ollama API error: status 400: {"error":"m does not support decision"}`)
}

func TestSystemOneProviderRejectionIsBadRequest(t *testing.T) {
	r := newDecisionRouter(&mockProviderClient{}, &rejectingDecisionClient{})
	if rec := postSystemOne(r, `{"model":"m","state":"x","questions":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAllowlistLetsOpenAIAndOllamaShareAServer covers one server registered as
// both an openai provider (chat) and an ollama provider limited by
// model_allowlist to the decision model: decisions must land on the ollama
// provider while chat for other models stays on the openai one.
func TestAllowlistLetsOpenAIAndOllamaShareAServer(t *testing.T) {
	dec := &decisionClient{}
	r := newOllamaHandlerTestRouter(&mockProviderClient{})
	oai := r.Providers["mock-provider"]
	oai.Name = "srv-openai"
	oai.Models = nil
	r.Providers = map[string]*Provider{"srv-openai": oai}
	r.ModelMap = map[string][]string{}
	r.ModelContext = map[string]int{}

	ol := &Provider{Name: "srv-ollama", ProviderType: "ollama", Client: dec, Enabled: true, Weight: 1,
		ModelAllowlist: []string{"clef-flash:latest"}}
	ol.Healthy.Store(true)
	r.Providers["srv-ollama"] = ol

	// Both providers discover the same server's models.
	discovered := []string{"clef-flash:latest", "qwen3.8:27b", "ornith-1.5:9b"}
	r.addProviderModels("srv-openai", discovered, oai, nil)
	r.addProviderModels("srv-ollama", discovered, ol, nil)

	if got := r.ModelMap["qwen3.8:27b"]; len(got) != 1 || got[0] != "srv-openai" {
		t.Fatalf("non-allowlisted model should only be on the openai provider, got %v", got)
	}
	if got := r.ModelMap["clef-flash:latest"]; len(got) != 2 {
		t.Fatalf("allowlisted model should be on both providers, got %v", got)
	}

	// The openai provider can't run decisions, so the ollama one must take it.
	name, err := r.getProviderForModelFiltered("clef-flash:latest", "", supportsDecision)
	if err != nil || name != "srv-ollama" {
		t.Fatalf("decision provider = %q, err %v; want srv-ollama", name, err)
	}
}
