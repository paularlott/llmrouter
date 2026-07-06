package router

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/snapshotkv"
	"github.com/paularlott/webchat"
)

const chatHistoryPrefix = "chat_history:"

// kvHistoryStore implements webchat.HistoryStore using snapshotkv.
// Conversations are stored as JSON blobs keyed by "chat_history:<id>".
// The List method scans all keys with the prefix, deserialises each,
// and returns summaries sorted by UpdatedAt descending.
type kvHistoryStore struct {
	db *snapshotkv.DB
}

// memoryHistoryStore is the fallback when storage is memory-only.
type memoryHistoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte // id → JSON StoredConversation
}

func newHistoryStore(store *storage.Store) webchat.HistoryStore {
	if store.IsMemory() {
		return &memoryHistoryStore{data: make(map[string][]byte)}
	}
	return &kvHistoryStore{db: store.DB()}
}

// -- snapshotkv implementation --

func (s *kvHistoryStore) List(ctx context.Context) ([]webchat.ConversationSummary, error) {
	keys := s.db.FindKeysByPrefix(chatHistoryPrefix)
	out := make([]webchat.ConversationSummary, 0, len(keys))
	for _, key := range keys {
		raw, err := s.db.Get(key)
		if err != nil {
			continue
		}
		data, ok := raw.([]byte)
		if !ok {
			continue
		}
		var conv webchat.StoredConversation
		if json.Unmarshal(data, &conv) != nil {
			continue
		}
		out = append(out, conv.ConversationSummary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (s *kvHistoryStore) Get(ctx context.Context, id string) (*webchat.StoredConversation, error) {
	raw, err := s.db.Get(chatHistoryPrefix + id)
	if err != nil {
		return nil, err
	}
	data, ok := raw.([]byte)
	if !ok {
		return nil, fmt.Errorf("unexpected data type for conversation %s", id)
	}
	var conv webchat.StoredConversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (s *kvHistoryStore) Save(ctx context.Context, conv *webchat.StoredConversation) error {
	data, err := json.Marshal(conv)
	if err != nil {
		return err
	}
	return s.db.Set(chatHistoryPrefix+conv.ID, data)
}

func (s *kvHistoryStore) Delete(ctx context.Context, id string) error {
	return s.db.Delete(chatHistoryPrefix + id)
}

// -- memory implementation --

func (s *memoryHistoryStore) List(ctx context.Context) ([]webchat.ConversationSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]webchat.ConversationSummary, 0, len(s.data))
	for _, data := range s.data {
		var conv webchat.StoredConversation
		if json.Unmarshal(data, &conv) != nil {
			continue
		}
		out = append(out, conv.ConversationSummary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

func (s *memoryHistoryStore) Get(ctx context.Context, id string) (*webchat.StoredConversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[id]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", id)
	}
	var conv webchat.StoredConversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (s *memoryHistoryStore) Save(ctx context.Context, conv *webchat.StoredConversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(conv)
	if err != nil {
		return err
	}
	s.data[conv.ID] = data
	return nil
}

func (s *memoryHistoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}
