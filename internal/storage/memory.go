package storage

import (
	"context"
	"fmt"
	"time"
)

// In-memory implementation
type MemoryStorage struct {
	responses map[string]*StoredResponse
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		responses: make(map[string]*StoredResponse),
	}
}

func (s *MemoryStorage) Store(ctx context.Context, response *StoredResponse) error {
	s.responses[response.ID] = response
	return nil
}

func (s *MemoryStorage) Get(ctx context.Context, id string) (*StoredResponse, error) {
	response, exists := s.responses[id]
	if !exists {
		return nil, fmt.Errorf("response not found")
	}
	return response, nil
}

func (s *MemoryStorage) List(ctx context.Context, filter ResponseFilter) ([]StoredResponse, error) {
	var responses []StoredResponse
	for _, response := range s.responses {
		responses = append(responses, *response)
		if filter.Limit > 0 && len(responses) >= filter.Limit {
			break
		}
	}
	return responses, nil
}

func (s *MemoryStorage) Delete(ctx context.Context, id string) error {
	delete(s.responses, id)
	return nil
}

func (s *MemoryStorage) UpdateStatus(ctx context.Context, id string, status ResponseStatus) error {
	response, exists := s.responses[id]
	if !exists {
		return fmt.Errorf("response not found")
	}
	response.Status = status
	response.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStorage) RunGC() error {
	return nil // No-op for memory storage
}

func (s *MemoryStorage) Close() error {
	return nil // No-op for memory storage
}