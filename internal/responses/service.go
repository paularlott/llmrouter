package responses

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

// entry tracks enough metadata for ListResponses without re-querying the client.
type entry struct {
	client    ai.Client
	model     string
	input     []any // input items used to create the response (for input_items endpoint)
	createdAt int64
	expiresAt time.Time
}

// Service delegates all Responses API operations to the originating ai.Client.
// Response IDs are mapped to their client in RAM (single-instance assumption).
type Service struct {
	mu      sync.RWMutex
	entries map[string]*entry // response ID -> entry
	ttl     time.Duration
}

func NewService(ttlDays int) *Service {
	ttl := time.Duration(ttlDays) * 24 * time.Hour
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	s := &Service{entries: make(map[string]*entry), ttl: ttl}
	go s.cleanup()
	return s
}

func (s *Service) cleanup() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, e := range s.entries {
			if now.After(e.expiresAt) {
				delete(s.entries, id)
			}
		}
		s.mu.Unlock()
	}
}

func (s *Service) CreateResponse(ctx context.Context, client ai.Client, req *openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	resp, err := client.CreateResponse(ctx, *req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.entries[resp.ID] = &entry{client: client, model: resp.Model, input: req.Input, createdAt: resp.CreatedAt, expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return resp, nil
}

func (s *Service) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	client, err := s.clientFor(id)
	if err != nil {
		return nil, err
	}
	return client.GetResponse(ctx, id)
}

func (s *Service) DeleteResponse(ctx context.Context, id string) error {
	client, err := s.clientFor(id)
	if err != nil {
		return err
	}
	if err := client.DeleteResponse(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.entries, id)
	s.mu.Unlock()
	return nil
}

func (s *Service) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	client, err := s.clientFor(id)
	if err != nil {
		return nil, err
	}
	return client.CancelResponse(ctx, id)
}

func (s *Service) CompactResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	client, err := s.clientFor(id)
	if err != nil {
		return nil, err
	}
	return client.CompactResponse(ctx, id)
}

// ListResponses returns a summary list from the in-RAM index.
func (s *Service) ListResponses(ctx context.Context) (*openai.ResponseListResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := make([]openai.ResponseObject, 0, len(s.entries))
	for id, e := range s.entries {
		data = append(data, openai.ResponseObject{
			ID:        id,
			Object:    "response",
			CreatedAt: e.createdAt,
			Model:     e.model,
		})
	}
	return &openai.ResponseListResponse{Object: "list", Data: data}, nil
}

func (s *Service) clientFor(id string) (ai.Client, error) {
	s.mu.RLock()
	e, ok := s.entries[id]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("response not found")
	}
	return e.client, nil
}

// GetInputItems returns the input items used to create a response (for the
// GET /responses/{id}/input_items endpoint).
func (s *Service) GetInputItems(_ context.Context, id string) ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("response not found")
	}
	if e.input == nil {
		return []any{}, nil
	}
	return e.input, nil
}

// Close is a no-op; cleanup is handled by the ai.Client instances.
func (s *Service) Close() {}

