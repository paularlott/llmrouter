package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/paularlott/mcp/openai"
)

// StoredConversation represents a conversation stored in the database
type StoredConversation struct {
	ID        string
	CreatedAt time.Time
	Metadata  map[string]interface{}
	Items     []openai.ConversationItem
}

// ConversationStorage defines the interface for conversation storage
type ConversationStorage interface {
	Store(ctx context.Context, conversation *StoredConversation) error
	Get(ctx context.Context, id string) (*StoredConversation, error)
	Delete(ctx context.Context, id string) error
	Update(ctx context.Context, id string, metadata map[string]interface{}) error

	// Item operations
	AddItems(ctx context.Context, conversationID string, items []openai.ConversationItem) error
	GetItems(ctx context.Context, conversationID string, after string, limit int, order string) ([]openai.ConversationItem, bool, error)
	GetItem(ctx context.Context, conversationID string, itemID string) (*openai.ConversationItem, error)
	DeleteItem(ctx context.Context, conversationID string, itemID string) error

	Close() error
}

// BadgerConversationStorage implements ConversationStorage using Badger
type BadgerConversationStorage struct {
	db  *badger.DB
	ttl time.Duration
}

// NewBadgerConversationStorage creates a new Badger-based conversation storage
func NewBadgerConversationStorage(path string, ttl time.Duration) (*BadgerConversationStorage, error) {
	opts := badger.DefaultOptions(path)
	opts.Logger = nil // Disable badger logging

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger: %w", err)
	}

	storage := &BadgerConversationStorage{
		db:  db,
		ttl: ttl,
	}

	return storage, nil
}

func (s *BadgerConversationStorage) Store(ctx context.Context, conversation *StoredConversation) error {
	key := []byte("conv:" + conversation.ID)

	data, err := json.Marshal(conversation)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry(key, data)
		if s.ttl > 0 {
			entry = entry.WithTTL(s.ttl)
		}
		return txn.SetEntry(entry)
	})
}

func (s *BadgerConversationStorage) Get(ctx context.Context, id string) (*StoredConversation, error) {
	key := []byte("conv:" + id)
	var conversation StoredConversation

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return fmt.Errorf("conversation not found")
			}
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &conversation)
		})
	})

	if err != nil {
		return nil, err
	}

	return &conversation, nil
}

func (s *BadgerConversationStorage) Delete(ctx context.Context, id string) error {
	key := []byte("conv:" + id)

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
}

func (s *BadgerConversationStorage) Update(ctx context.Context, id string, metadata map[string]interface{}) error {
	conversation, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	conversation.Metadata = metadata
	return s.Store(ctx, conversation)
}

func (s *BadgerConversationStorage) AddItems(ctx context.Context, conversationID string, items []openai.ConversationItem) error {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}

	conversation.Items = append(conversation.Items, items...)
	return s.Store(ctx, conversation)
}

func (s *BadgerConversationStorage) GetItems(ctx context.Context, conversationID string, after string, limit int, order string) ([]openai.ConversationItem, bool, error) {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, false, err
	}

	items := conversation.Items

	// Handle order
	if order == "asc" {
		// Items are already in ascending order (as added)
	} else {
		// Default is desc - reverse the items
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}

	// Handle pagination with 'after'
	startIdx := 0
	if after != "" {
		for i, item := range items {
			if item.ID == after {
				startIdx = i + 1
				break
			}
		}
	}

	// Apply limit
	if limit <= 0 {
		limit = 20 // Default
	}

	endIdx := startIdx + limit
	hasMore := endIdx < len(items)
	if endIdx > len(items) {
		endIdx = len(items)
	}

	if startIdx >= len(items) {
		return []openai.ConversationItem{}, false, nil
	}

	return items[startIdx:endIdx], hasMore, nil
}

func (s *BadgerConversationStorage) GetItem(ctx context.Context, conversationID string, itemID string) (*openai.ConversationItem, error) {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	for _, item := range conversation.Items {
		if item.ID == itemID {
			return &item, nil
		}
	}

	return nil, fmt.Errorf("item not found")
}

func (s *BadgerConversationStorage) DeleteItem(ctx context.Context, conversationID string, itemID string) error {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}

	// Find and remove the item
	newItems := make([]openai.ConversationItem, 0, len(conversation.Items))
	found := false
	for _, item := range conversation.Items {
		if item.ID != itemID {
			newItems = append(newItems, item)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("item not found")
	}

	conversation.Items = newItems
	return s.Store(ctx, conversation)
}

func (s *BadgerConversationStorage) Close() error {
	return s.db.Close()
}

// MemoryConversationStorage implements ConversationStorage using in-memory storage
type MemoryConversationStorage struct {
	conversations map[string]*StoredConversation
}

// NewMemoryConversationStorage creates a new memory-based conversation storage
func NewMemoryConversationStorage() *MemoryConversationStorage {
	return &MemoryConversationStorage{
		conversations: make(map[string]*StoredConversation),
	}
}

func (s *MemoryConversationStorage) Store(ctx context.Context, conversation *StoredConversation) error {
	s.conversations[conversation.ID] = conversation
	return nil
}

func (s *MemoryConversationStorage) Get(ctx context.Context, id string) (*StoredConversation, error) {
	conversation, ok := s.conversations[id]
	if !ok {
		return nil, fmt.Errorf("conversation not found")
	}
	return conversation, nil
}

func (s *MemoryConversationStorage) Delete(ctx context.Context, id string) error {
	delete(s.conversations, id)
	return nil
}

func (s *MemoryConversationStorage) Update(ctx context.Context, id string, metadata map[string]interface{}) error {
	conversation, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	conversation.Metadata = metadata
	return s.Store(ctx, conversation)
}

func (s *MemoryConversationStorage) AddItems(ctx context.Context, conversationID string, items []openai.ConversationItem) error {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}

	conversation.Items = append(conversation.Items, items...)
	return s.Store(ctx, conversation)
}

func (s *MemoryConversationStorage) GetItems(ctx context.Context, conversationID string, after string, limit int, order string) ([]openai.ConversationItem, bool, error) {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, false, err
	}

	items := conversation.Items

	// Handle order
	if order == "asc" {
		// Items are already in ascending order
	} else {
		// Default is desc - reverse the items
		reversed := make([]openai.ConversationItem, len(items))
		for i, item := range items {
			reversed[len(items)-1-i] = item
		}
		items = reversed
	}

	// Handle pagination with 'after'
	startIdx := 0
	if after != "" {
		for i, item := range items {
			if item.ID == after {
				startIdx = i + 1
				break
			}
		}
	}

	// Apply limit
	if limit <= 0 {
		limit = 20 // Default
	}

	endIdx := startIdx + limit
	hasMore := endIdx < len(items)
	if endIdx > len(items) {
		endIdx = len(items)
	}

	if startIdx >= len(items) {
		return []openai.ConversationItem{}, false, nil
	}

	return items[startIdx:endIdx], hasMore, nil
}

func (s *MemoryConversationStorage) GetItem(ctx context.Context, conversationID string, itemID string) (*openai.ConversationItem, error) {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	for _, item := range conversation.Items {
		if item.ID == itemID {
			return &item, nil
		}
	}

	return nil, fmt.Errorf("item not found")
}

func (s *MemoryConversationStorage) DeleteItem(ctx context.Context, conversationID string, itemID string) error {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}

	// Find and remove the item
	newItems := make([]openai.ConversationItem, 0, len(conversation.Items))
	found := false
	for _, item := range conversation.Items {
		if item.ID != itemID {
			newItems = append(newItems, item)
		} else {
			found = true
		}
	}

	if !found {
		return fmt.Errorf("item not found")
	}

	conversation.Items = newItems
	return s.Store(ctx, conversation)
}

func (s *MemoryConversationStorage) Close() error {
	return nil
}
