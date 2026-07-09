package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/responses"
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
	compactResp   *openai.ResponseObject
	createCalls   int
	getCalls      int
	deleteCalls   int
	cancelCalls   int
	compactCalls  int
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
func (m *respMockClient) StreamResponse(context.Context, openai.CreateResponseRequest) *ai.ResponseStream {
	return nil
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
func (m *respMockClient) CompactResponse(_ context.Context, _ string) (*openai.ResponseObject, error) {
	m.compactCalls++
	if m.compactResp != nil {
		return m.compactResp, nil
	}
	return &openai.ResponseObject{ID: "resp_test", Object: "response", Status: "completed"}, nil
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
