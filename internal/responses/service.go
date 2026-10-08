package responses

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

var (
	// ErrNotFound is returned for a response this router doesn't know.
	ErrNotFound = errors.New("response not found")
	// ErrOtherProvider is returned when a request refers to a response
	// created through another provider than the one its model routes to:
	// responses are stored per provider, so it can't be continued there.
	ErrOtherProvider = errors.New("previous_response_id belongs to a response from another provider; continue it with a model on the same provider")
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

// NewService creates the responses index. Entries expire ttl after their
// last use, as the shared response store's do (see types.ResponsesConfig.TTL).
func NewService(ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = types.ResponsesConfig{}.TTL()
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
	if err := s.checkPrevious(client, req.PreviousResponseID); err != nil {
		return nil, err
	}
	resp, err := client.CreateResponse(ctx, *req)
	if err != nil {
		return nil, err
	}
	s.Track(client, resp, req.Input)
	return resp, nil
}

// StreamResponse starts streaming a response from client. Pass the
// response from its response.completed event to Track, so the router can
// serve it afterwards.
func (s *Service) StreamResponse(ctx context.Context, client ai.Client, req *openai.CreateResponseRequest) (*ai.ResponseStream, error) {
	if err := s.checkPrevious(client, req.PreviousResponseID); err != nil {
		return nil, err
	}
	return client.StreamResponse(ctx, *req), nil
}

// Track records a response created through client, so it can be fetched,
// continued, compacted, cancelled and deleted through the router.
func (s *Service) Track(client ai.Client, resp *openai.ResponseObject, input []any) {
	if resp == nil || resp.ID == "" {
		return
	}
	s.mu.Lock()
	s.entries[resp.ID] = &entry{client: client, model: resp.Model, input: input, createdAt: resp.CreatedAt, expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
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

// CompactResponse compacts a conversation with client, which must be the
// client of req.PreviousResponseID when that is a response this router
// created.
func (s *Service) CompactResponse(ctx context.Context, client ai.Client, req ai.CompactResponseRequest) (*ai.CompactedResponse, error) {
	if err := s.checkPrevious(client, req.PreviousResponseID); err != nil {
		return nil, err
	}
	return client.CompactResponse(ctx, req)
}

// CompactResponseByID compacts the conversation of response id with its own
// client and model (the legacy POST /responses/{id}/compact form).
func (s *Service) CompactResponseByID(ctx context.Context, id string) (*ai.CompactedResponse, error) {
	e, ok := s.use(id)
	if !ok {
		return nil, ErrNotFound
	}
	return e.client.CompactResponse(ctx, ai.CompactResponseRequest{Model: e.model, PreviousResponseID: id})
}

// ClientFor returns the client that created response id.
func (s *Service) ClientFor(id string) (ai.Client, error) {
	return s.clientFor(id)
}

// checkPrevious rejects continuing a response this router created through
// a different client. Unknown IDs are left to the client, which reports
// them as not found.
func (s *Service) checkPrevious(client ai.Client, previousID string) error {
	if previousID == "" {
		return nil
	}
	e, ok := s.use(previousID)
	if ok && e.client != client {
		return ErrOtherProvider
	}
	return nil
}

// use returns the entry for id, restarting its expiry: like the response
// store, the index keeps responses for the TTL after their last use. (The
// store also refreshes the earlier responses of a continued conversation;
// the index only refreshes the ones requests name.)
func (s *Service) use(id string) (*entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if ok {
		e.expiresAt = time.Now().Add(s.ttl)
	}
	return e, ok
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
	e, ok := s.use(id)
	if !ok {
		return nil, ErrNotFound
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
		return nil, ErrNotFound
	}
	if e.input == nil {
		return []any{}, nil
	}
	return e.input, nil
}

// Close is a no-op; cleanup is handled by the ai.Client instances.
func (s *Service) Close() {}
