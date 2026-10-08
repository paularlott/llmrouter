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
	"testing"
	"time"

	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// respMockClient is a configurable ai.Client for the responses HTTP handlers.
type respMockClient struct {
	provider      string
	createResp    *openai.ResponseObject
	createErr     error
	getResp       *openai.ResponseObject
	getErr        error
	deleteErr     error
	cancelResp    *openai.ResponseObject
	compactResp   *ai.CompactedResponse
	createCalls   int
	getCalls      int
	deleteCalls   int
	cancelCalls   int
	compactCalls  int
	lastCompact   ai.CompactResponseRequest
	streamEvents  []openai.ResponseStreamEvent
	streamErr     error
	streamCalls   int
	lastStreamReq openai.CreateResponseRequest
	lastCreateReq openai.CreateResponseRequest
}

func (m *respMockClient) Provider() string               { return m.provider }
func (m *respMockClient) SupportsCapability(string) bool { return false }
func (m *respMockClient) Close() error                   { return nil }
func (m *respMockClient) GetModels(context.Context) (*ai.ModelsResponse, error) {
	return &ai.ModelsResponse{}, nil
}
func (m *respMockClient) ChatCompletion(context.Context, openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return nil, nil
}
func (m *respMockClient) StreamChatCompletion(context.Context, openai.ChatCompletionRequest) *ai.ChatStream {
	return nil
}
func (m *respMockClient) CreateEmbedding(context.Context, openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return nil, nil
}
func (m *respMockClient) StreamResponse(ctx context.Context, req openai.CreateResponseRequest) *ai.ResponseStream {
	m.streamCalls++
	m.lastStreamReq = req
	events := make(chan openai.ResponseStreamEvent, len(m.streamEvents))
	errs := make(chan error, 1)
	for _, e := range m.streamEvents {
		events <- e
	}
	close(events)
	if m.streamErr != nil {
		errs <- m.streamErr
	}
	close(errs)
	return openai.NewResponseStream(ctx, events, errs)
}
func (m *respMockClient) CreateResponse(_ context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	m.createCalls++
	m.lastCreateReq = req
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createResp != nil {
		return m.createResp, nil
	}
	return &openai.ResponseObject{ID: "resp_test", Object: "response", Status: "completed", Model: req.Model}, nil
}
func (m *respMockClient) GetResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResp != nil {
		return m.getResp, nil
	}
	return &openai.ResponseObject{ID: "resp_test", Object: "response", Status: "completed"}, nil
}
func (m *respMockClient) CancelResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	m.cancelCalls++
	if m.cancelResp != nil {
		return m.cancelResp, nil
	}
	return &openai.ResponseObject{ID: "resp_test", Object: "response", Status: "cancelled"}, nil
}
func (m *respMockClient) DeleteResponse(_ context.Context, _ string) error {
	m.deleteCalls++
	return m.deleteErr
}
func (m *respMockClient) CompactResponse(_ context.Context, req ai.CompactResponseRequest) (*ai.CompactedResponse, error) {
	m.compactCalls++
	m.lastCompact = req
	if m.compactResp != nil {
		return m.compactResp, nil
	}
	return &ai.CompactedResponse{ID: "cmp_test", Object: "response.compaction"}, nil
}

// newResponsesRouter builds a Router wired with a real responses service and a
// single provider serving `model`, backed by client.
func newResponsesRouter(model string, client ai.Client) *Router {
	r := &Router{
		Providers:        make(map[string]*Provider),
		ModelMap:         make(map[string][]string),
		ModelTags:        make(map[string][]string),
		logger:           &testLogger{},
		responsesService: responses.NewService(0),
	}
	p := &Provider{Name: "p1", ProviderType: "openai", Client: client, Enabled: true, Weight: 1.0}
	p.Healthy.Store(true)
	r.Providers["p1"] = p
	r.ModelMap[model] = []string{"p1"}
	return r
}

// seedResponse creates a response via the handler so the service tracks it,
// returning the response ID for follow-up get/delete/cancel/compact tests.
func seedResponse(t *testing.T, r *Router, model string) string {
	t.Helper()
	body := []byte(`{"model":"` + model + `","input":[{"type":"message","role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("seed create failed: %d %s", w.Code, w.Body.String())
	}
	var resp openai.ResponseObject
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("seed unmarshal: %v", err)
	}
	return resp.ID
}

func TestHandleCreateResponse_HappyPath(t *testing.T) {
	c := &respMockClient{provider: "p1"}
	r := newResponsesRouter("m1", c)

	body := []byte(`{"model":"m1","input":[{"type":"message","role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if c.createCalls != 1 {
		t.Errorf("create calls = %d, want 1", c.createCalls)
	}
	var resp openai.ResponseObject
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "resp_test" || resp.Object != "response" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestHandleCreateResponse_UnknownModel(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)

	body := []byte(`{"model":"unknown","input":[]}`)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleCreateResponse_InvalidJSON(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte("{not json")))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleCreateResponse_ClientError(t *testing.T) {
	c := &respMockClient{createErr: errors.New("upstream failure")}
	r := newResponsesRouter("m1", c)

	body := []byte(`{"model":"m1","input":[]}`)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// Per the Responses API spec, `input` may be a bare string. The create handler
// must accept it (normalised to a single user message) and return 200.
func TestHandleCreateResponse_StringInput(t *testing.T) {
	c := &respMockClient{provider: "p1"}
	r := newResponsesRouter("m1", c)

	body := []byte(`{"model":"m1","input":"Tell me a three sentence bedtime story about a unicorn."}`)
	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCreateResponse(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if c.createCalls != 1 {
		t.Errorf("create calls = %d, want 1", c.createCalls)
	}
	// The model received the request; input normalised to a single user message.
	if len(c.lastCreateReq.Input) != 1 {
		t.Fatalf("normalised input len = %d, want 1", len(c.lastCreateReq.Input))
	}
}

func TestHandleGetResponse_HappyAndNotFound(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	id := seedResponse(t, r, "m1")

	// Existing → 200.
	req := httptest.NewRequest("GET", "/v1/responses/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	r.HandleGetResponse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if c.getCalls != 1 {
		t.Errorf("get calls = %d, want 1", c.getCalls)
	}

	// Missing → 404.
	req2 := httptest.NewRequest("GET", "/v1/responses/resp_missing", nil)
	req2.SetPathValue("id", "resp_missing")
	w2 := httptest.NewRecorder()
	r.HandleGetResponse(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w2.Code)
	}
}

func TestHandleDeleteResponse_HappyAndNotFound(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	id := seedResponse(t, r, "m1")

	req := httptest.NewRequest("DELETE", "/v1/responses/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	r.HandleDeleteResponse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if c.deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", c.deleteCalls)
	}
	// Spec: delete returns {id, object:"response", deleted:true}.
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["id"] != id || body["object"] != "response" || body["deleted"] != true {
		t.Errorf("delete body = %#v", body)
	}

	// After delete, the entry is gone → 404.
	req2 := httptest.NewRequest("DELETE", "/v1/responses/"+id, nil)
	req2.SetPathValue("id", id)
	w2 := httptest.NewRecorder()
	r.HandleDeleteResponse(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w2.Code)
	}
}

func TestHandleCancelResponse(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	id := seedResponse(t, r, "m1")

	req := httptest.NewRequest("POST", "/v1/responses/"+id+"/cancel", nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	r.HandleCancelResponse(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if c.cancelCalls != 1 {
		t.Errorf("cancel calls = %d, want 1", c.cancelCalls)
	}

	// Missing → 404.
	req2 := httptest.NewRequest("POST", "/v1/responses/resp_missing/cancel", nil)
	req2.SetPathValue("id", "resp_missing")
	w2 := httptest.NewRecorder()
	r.HandleCancelResponse(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w2.Code)
	}
}

func TestHandleCompactResponses(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	id := seedResponse(t, r, "m1")

	req := httptest.NewRequest("POST", "/v1/responses/"+id+"/compact", nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	r.HandleCompactResponses(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if c.compactCalls != 1 {
		t.Errorf("compact calls = %d, want 1", c.compactCalls)
	}
}

func TestHandleListResponses(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	seedResponse(t, r, "m1")

	req := httptest.NewRequest("GET", "/v1/responses", nil)
	w := httptest.NewRecorder()
	r.HandleListResponses(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var list openai.ResponseListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Object != "list" {
		t.Errorf("Object = %q", list.Object)
	}
	if len(list.Data) != 1 {
		t.Errorf("Data len = %d, want 1", len(list.Data))
	}
}

func TestHandleListResponseInputItems(t *testing.T) {
	c := &respMockClient{}
	r := newResponsesRouter("m1", c)
	id := seedResponse(t, r, "m1")

	req := httptest.NewRequest("GET", "/v1/responses/"+id+"/input_items", nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	r.HandleListResponseInputItems(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []any  `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Object != "list" {
		t.Errorf("Object = %q", body.Object)
	}
	if len(body.Data) != 1 {
		t.Fatalf("Data len = %d, want 1 (the seeded user message)", len(body.Data))
	}

	// Missing response → 404.
	req2 := httptest.NewRequest("GET", "/v1/responses/missing/input_items", nil)
	req2.SetPathValue("id", "missing")
	w2 := httptest.NewRecorder()
	r.HandleListResponseInputItems(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w2.Code)
	}
}

func TestHandleCountInputTokens(t *testing.T) {
	r := &Router{logger: &testLogger{}}

	// String input + instructions → a positive token estimate.
	body := []byte(`{"model":"m1","instructions":"be helpful","input":"Tell me a three sentence bedtime story about a unicorn."}`)
	req := httptest.NewRequest("POST", "/v1/responses/input_tokens", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.HandleCountInputTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Object      string `json:"object"`
		InputTokens int    `json:"input_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Object != "response.input_tokens" {
		t.Errorf("Object = %q", resp.Object)
	}
	if resp.InputTokens <= 0 {
		t.Errorf("InputTokens = %d, want > 0", resp.InputTokens)
	}
}

func TestHandleCountInputTokens_InvalidJSON(t *testing.T) {
	r := &Router{logger: &testLogger{}}
	req := httptest.NewRequest("POST", "/v1/responses/input_tokens", bytes.NewReader([]byte("{bad")))
	w := httptest.NewRecorder()
	r.HandleCountInputTokens(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// newTwoProviderResponsesRouter serves m1 from provider p1 and m2 from p2.
func newTwoProviderResponsesRouter(c1, c2 ai.Client) *Router {
	r := newResponsesRouter("m1", c1)
	p := &Provider{Name: "p2", ProviderType: "openai", Client: c2, Enabled: true, Weight: 1.0}
	p.Healthy.Store(true)
	r.Providers["p2"] = p
	r.ModelMap["m2"] = []string{"p2"}
	return r
}

func postJSON(r *Router, handler func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleCompactResponse(t *testing.T) {
	c1 := &respMockClient{provider: "p1", createResp: &openai.ResponseObject{ID: "resp_p1", Object: "response", Status: "completed", Model: "m1"}}
	c2 := &respMockClient{provider: "p2"}
	r := newTwoProviderResponsesRouter(c1, c2)
	id := seedResponse(t, r, "m1")

	// Compacts with the model's provider, which created the response
	w := postJSON(r, r.HandleCompactResponse, "/v1/responses/compact", `{"model":"m1","previous_response_id":"`+id+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got ai.CompactedResponse
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Object != "response.compaction" || c1.compactCalls != 1 || c1.lastCompact.PreviousResponseID != id || c1.lastCompact.Model != "m1" {
		t.Errorf("compaction = %+v, calls = %d, request = %+v", got, c1.compactCalls, c1.lastCompact)
	}

	// Input only: any provider
	if w := postJSON(r, r.HandleCompactResponse, "/v1/responses/compact", `{"model":"m2","input":[{"role":"user","content":"hi"}]}`); w.Code != http.StatusOK || c2.compactCalls != 1 {
		t.Errorf("input-only compact: status = %d, p2 calls = %d", w.Code, c2.compactCalls)
	}

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"another provider's response": {`{"model":"m2","previous_response_id":"` + id + `"}`, http.StatusBadRequest},
		"no model":                    {`{"previous_response_id":"` + id + `"}`, http.StatusBadRequest},
		"nothing to compact":          {`{"model":"m1"}`, http.StatusBadRequest},
		"unknown model":               {`{"model":"nope","input":[{"role":"user","content":"hi"}]}`, http.StatusNotFound},
	} {
		if w := postJSON(r, r.HandleCompactResponse, "/v1/responses/compact", tc.body); w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestHandleCreateResponse_PreviousResponseErrors(t *testing.T) {
	c1 := &respMockClient{provider: "p1", createResp: &openai.ResponseObject{ID: "resp_p1", Object: "response", Status: "completed", Model: "m1"}}
	c2 := &respMockClient{provider: "p2", createErr: fmt.Errorf("previous %w: resp_gone", ai.ErrResponseNotFound)}
	r := newTwoProviderResponsesRouter(c1, c2)
	id := seedResponse(t, r, "m1")

	// Continuing with another provider's model is a client error, not a 500
	w := postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m2","previous_response_id":"`+id+`","input":"again"}`)
	if w.Code != http.StatusBadRequest || c2.createCalls != 0 {
		t.Errorf("other provider: status = %d (%s), p2 calls = %d", w.Code, w.Body.String(), c2.createCalls)
	}
	// A previous response the client doesn't know is a 404
	w = postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m2","previous_response_id":"resp_gone","input":"again"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown previous: status = %d (%s), want 404", w.Code, w.Body.String())
	}
}

func streamEvent(typ string, fields map[string]any) openai.ResponseStreamEvent {
	fields["type"] = typ
	data, _ := json.Marshal(fields)
	return openai.ResponseStreamEvent{Type: typ, Data: data}
}

func TestHandleCreateResponse_Stream(t *testing.T) {
	completed := map[string]any{"id": "resp_stream", "object": "response", "status": "completed", "model": "m1"}
	c := &respMockClient{provider: "p1", streamEvents: []openai.ResponseStreamEvent{
		streamEvent("response.created", map[string]any{"response": map[string]any{"id": "resp_stream", "status": "in_progress"}}),
		streamEvent("response.output_text.delta", map[string]any{"delta": "Hel"}),
		streamEvent("response.output_text.delta", map[string]any{"delta": "lo"}),
		streamEvent("response.completed", map[string]any{"response": completed}),
	}}
	r := newResponsesRouter("m1", c)

	w := postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m1","input":"hi","stream":true}`)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content type = %q, body = %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"event: response.created\ndata: ", "event: response.output_text.delta\ndata: {\"delta\":\"Hel\"", "event: response.completed\ndata: "} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q:\n%s", want, body)
		}
	}
	if c.streamCalls != 1 || c.createCalls != 0 {
		t.Errorf("stream calls = %d, create calls = %d", c.streamCalls, c.createCalls)
	}
	if _, sent := c.lastStreamReq.ExtraBody["stream"]; sent {
		t.Error("stream flag forwarded upstream")
	}

	// The completed response is tracked: it can be fetched and continued
	req := httptest.NewRequest("GET", "/v1/responses/resp_stream", nil)
	req.SetPathValue("id", "resp_stream")
	gw := httptest.NewRecorder()
	r.HandleGetResponse(gw, req)
	if gw.Code != http.StatusOK {
		t.Errorf("get streamed response: status = %d", gw.Code)
	}
}

func TestHandleCreateResponse_StreamFlagNotForwardedWhenFalse(t *testing.T) {
	c := &respMockClient{provider: "p1"}
	r := newResponsesRouter("m1", c)
	if w := postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m1","input":"hi","stream":false}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if _, sent := c.lastCreateReq.ExtraBody["stream"]; sent || c.streamCalls != 0 {
		t.Errorf("stream flag forwarded (%v) or streamed (%d)", c.lastCreateReq.ExtraBody, c.streamCalls)
	}
}

func TestHandleCreateResponse_StreamErrors(t *testing.T) {
	// Failing before any event: a proper HTTP status, not an SSE stream
	notFound := &respMockClient{provider: "p1", streamErr: fmt.Errorf("previous %w: resp_gone", ai.ErrResponseNotFound)}
	r := newResponsesRouter("m1", notFound)
	w := postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m1","previous_response_id":"resp_gone","input":"hi","stream":true}`)
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") == "text/event-stream" {
		t.Errorf("early error: status = %d, content type = %q", w.Code, w.Header().Get("Content-Type"))
	}

	// Failing mid-stream: the stream ends with an error event
	mid := &respMockClient{provider: "p1", streamErr: fmt.Errorf("upstream broke"), streamEvents: []openai.ResponseStreamEvent{
		streamEvent("response.created", map[string]any{"response": map[string]any{"id": "resp_mid"}}),
	}}
	r = newResponsesRouter("m1", mid)
	w = postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m1","input":"hi","stream":true}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "event: error\ndata: ") || !strings.Contains(w.Body.String(), "upstream broke") {
		t.Errorf("mid-stream error: status = %d, body = %s", w.Code, w.Body.String())
	}

	// Continuing another provider's response: 400 before streaming
	c1 := &respMockClient{provider: "p1", createResp: &openai.ResponseObject{ID: "resp_p1", Object: "response", Status: "completed", Model: "m1"}}
	c2 := &respMockClient{provider: "p2"}
	r = newTwoProviderResponsesRouter(c1, c2)
	id := seedResponse(t, r, "m1")
	w = postJSON(r, r.HandleCreateResponse, "/v1/responses", `{"model":"m2","previous_response_id":"`+id+`","input":"hi","stream":true}`)
	if w.Code != http.StatusBadRequest || c2.streamCalls != 0 {
		t.Errorf("other provider: status = %d, p2 stream calls = %d", w.Code, c2.streamCalls)
	}
}

// The shared response store takes its expiry and limits from [responses].
func TestSetResponseStore_UsesResponsesConfig(t *testing.T) {
	defer setResponseStore(types.ResponsesConfig{})
	setResponseStore(types.ResponsesConfig{MaxResponses: 2})
	store, ok := currentResponseStore().(*openai.MemoryResponseStore)
	if !ok {
		t.Fatalf("store = %T", currentResponseStore())
	}
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := store.Save(ctx, &openai.StoredResponse{ID: id, Status: openai.StatusCompleted}); err != nil {
			t.Fatal(err)
		}
	}
	if store.Len() != 2 {
		t.Errorf("store holds %d responses, want max_responses = 2", store.Len())
	}

	setResponseStore(types.ResponsesConfig{MaxMemoryMB: -1, MaxResponses: -1})
	big := currentResponseStore().(*openai.MemoryResponseStore)
	for i := 0; i < 20; i++ {
		big.Save(ctx, &openai.StoredResponse{ID: fmt.Sprint(i), Status: openai.StatusCompleted})
	}
	if big.Len() != 20 {
		t.Errorf("unlimited store holds %d, want 20", big.Len())
	}
}

func TestResponsesConfig_TTL(t *testing.T) {
	if got := (types.ResponsesConfig{}).TTL(); got != 30*24*time.Hour {
		t.Errorf("default = %v", got)
	}
	if got := (types.ResponsesConfig{TTLDays: 2}).TTL(); got != 48*time.Hour {
		t.Errorf("2 days = %v", got)
	}
}
