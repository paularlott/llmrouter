package responses

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// mockClient is a configurable ai.Client that records calls and returns canned
// responses. Only the Responses-API methods are exercised here; the rest return
// zero values.
type mockClient struct {
	mu sync.Mutex

	provider string

	createErr     error
	getErr        error
	deleteErr     error
	cancelErr     error
	compactErr    error
	createResp    *openai.ResponseObject
	getResp       *openai.ResponseObject
	cancelResp    *openai.ResponseObject
	compactResp   *openai.ResponseObject
	createCalls   int
	getCalls      []string
	deleteCalls   []string
	cancelCalls   []string
	compactCalls  []string
	lastCreateReq openai.CreateResponseRequest
}

func (m *mockClient) Provider() string               { return m.provider }
func (m *mockClient) SupportsCapability(string) bool { return false }
func (m *mockClient) Close() error                   { return nil }
func (m *mockClient) GetModels(context.Context) (*ai.ModelsResponse, error) {
	return &ai.ModelsResponse{}, nil
}
func (m *mockClient) ChatCompletion(context.Context, openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	return nil, nil
}
func (m *mockClient) StreamChatCompletion(context.Context, openai.ChatCompletionRequest) *ai.ChatStream {
	return nil
}
func (m *mockClient) CreateEmbedding(context.Context, openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return nil, nil
}
func (m *mockClient) StreamResponse(context.Context, openai.CreateResponseRequest) *ai.ResponseStream {
	return nil
}

func (m *mockClient) CreateResponse(_ context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	m.lastCreateReq = req
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.createResp != nil {
		return m.createResp, nil
	}
	return &openai.ResponseObject{ID: "resp_123", Object: "response", Status: "completed", CreatedAt: 1, Model: req.Model}, nil
}
func (m *mockClient) GetResponse(_ context.Context, id string) (*openai.ResponseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls = append(m.getCalls, id)
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getResp != nil {
		return m.getResp, nil
	}
	return &openai.ResponseObject{ID: id, Object: "response", Status: "completed"}, nil
}
func (m *mockClient) CancelResponse(_ context.Context, id string) (*openai.ResponseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelCalls = append(m.cancelCalls, id)
	if m.cancelErr != nil {
		return nil, m.cancelErr
	}
	if m.cancelResp != nil {
		return m.cancelResp, nil
	}
	return &openai.ResponseObject{ID: id, Object: "response", Status: "cancelled"}, nil
}
func (m *mockClient) DeleteResponse(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls = append(m.deleteCalls, id)
	return m.deleteErr
}
func (m *mockClient) CompactResponse(_ context.Context, id string) (*openai.ResponseObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compactCalls = append(m.compactCalls, id)
	if m.compactErr != nil {
		return nil, m.compactErr
	}
	if m.compactResp != nil {
		return m.compactResp, nil
	}
	return &openai.ResponseObject{ID: id, Object: "response", Status: "completed"}, nil
}

func newTestService() *Service {
	return NewService(0) // 0 → default 30-day TTL
}

func TestService_CreateThenGet_RoutesToOriginClient(t *testing.T) {
	s := newTestService()
	c := &mockClient{provider: "p1"}

	resp, err := s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if resp.ID != "resp_123" {
		t.Fatalf("ID = %q", resp.ID)
	}

	got, err := s.GetResponse(context.Background(), "resp_123")
	if err != nil {
		t.Fatalf("GetResponse: %v", err)
	}
	if got.ID != "resp_123" {
		t.Errorf("got ID = %q", got.ID)
	}
	if len(c.getCalls) != 1 || c.getCalls[0] != "resp_123" {
		t.Errorf("client get calls = %v", c.getCalls)
	}
}

func TestService_GetResponse_NotFound(t *testing.T) {
	s := newTestService()
	_, err := s.GetResponse(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestService_Delete_RemovesEntry(t *testing.T) {
	s := newTestService()
	c := &mockClient{}
	s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})

	if err := s.DeleteResponse(context.Background(), "resp_123"); err != nil {
		t.Fatalf("DeleteResponse: %v", err)
	}
	// After delete, a subsequent get should report not found.
	if _, err := s.GetResponse(context.Background(), "resp_123"); err == nil {
		t.Fatal("expected not-found after delete")
	}
}

func TestService_DeleteResponse_NotFound(t *testing.T) {
	s := newTestService()
	if err := s.DeleteResponse(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestService_DeleteResponse_PropagatesClientError(t *testing.T) {
	s := newTestService()
	c := &mockClient{deleteErr: errors.New("upstream boom")}
	s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})
	if err := s.DeleteResponse(context.Background(), "resp_123"); err == nil {
		t.Fatal("expected upstream error to propagate")
	}
}

func TestService_ListResponses(t *testing.T) {
	s := newTestService()
	c := &mockClient{createResp: &openai.ResponseObject{ID: "resp_a", Object: "response", Status: "completed", CreatedAt: 10, Model: "m1"}}
	s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})

	list, err := s.ListResponses(context.Background())
	if err != nil {
		t.Fatalf("ListResponses: %v", err)
	}
	if list.Object != "list" {
		t.Errorf("Object = %q", list.Object)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "resp_a" {
		t.Errorf("Data = %+v", list.Data)
	}
}

func TestService_CancelResponse_Delegates(t *testing.T) {
	s := newTestService()
	c := &mockClient{}
	s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})

	got, err := s.CancelResponse(context.Background(), "resp_123")
	if err != nil {
		t.Fatalf("CancelResponse: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("Status = %q", got.Status)
	}
}

func TestService_CompactResponse_Delegates(t *testing.T) {
	s := newTestService()
	c := &mockClient{}
	s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"})

	if _, err := s.CompactResponse(context.Background(), "resp_123"); err != nil {
		t.Fatalf("CompactResponse: %v", err)
	}
	if len(c.compactCalls) != 1 || c.compactCalls[0] != "resp_123" {
		t.Errorf("compact calls = %v", c.compactCalls)
	}
}

func TestService_CancelResponse_NotFound(t *testing.T) {
	s := newTestService()
	if _, err := s.CancelResponse(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestService_CompactResponse_NotFound(t *testing.T) {
	s := newTestService()
	if _, err := s.CompactResponse(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestService_CreateResponse_PropagatesClientError(t *testing.T) {
	s := newTestService()
	c := &mockClient{createErr: errors.New("nope")}
	if _, err := s.CreateResponse(context.Background(), c, &openai.CreateResponseRequest{Model: "m1"}); err == nil {
		t.Fatal("expected client error to propagate")
	}
}
