package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/paularlott/snapshotkv"
	"github.com/paularlott/mcp/ai/openai"
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
	AddItems(ctx context.Context, conversationID string, items []openai.ConversationItem) error
	GetItems(ctx context.Context, conversationID string, after string, limit int, order string) ([]openai.ConversationItem, bool, error)
	GetItem(ctx context.Context, conversationID string, itemID string) (*openai.ConversationItem, error)
	DeleteItem(ctx context.Context, conversationID string, itemID string) error
}

// conversationMetadata is stored in the snapshotkv document (without items)
type conversationMetadata struct {
	ID        string                 `json:"id"`
	CreatedAt time.Time              `json:"created_at"`
	Metadata  map[string]interface{} `json:"metadata"`
	ItemCount int                    `json:"item_count"`
}

const conversationKeyPrefix = "conversations:"

// SnapshotConversationStorage implements ConversationStorage using snapshotkv
type SnapshotConversationStorage struct {
	db  *snapshotkv.DB
	ttl time.Duration
}

// NewSnapshotConversationStorage creates a new snapshotkv-based conversation storage
func NewSnapshotConversationStorage(db *snapshotkv.DB, ttl time.Duration) *SnapshotConversationStorage {
	return &SnapshotConversationStorage{db: db, ttl: ttl}
}

func (s *SnapshotConversationStorage) conversationKey(id string) string {
	return conversationKeyPrefix + id
}

func (s *SnapshotConversationStorage) Store(ctx context.Context, conversation *StoredConversation) error {
	key := s.conversationKey(conversation.ID)

	// Serialize items to JSON for blob storage
	itemsData, err := json.Marshal(conversation.Items)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation items: %w", err)
	}

	// Create metadata (stored in document)
	meta := conversationMetadata{
		ID:        conversation.ID,
		CreatedAt: conversation.CreatedAt,
		Metadata:  conversation.Metadata,
		ItemCount: len(conversation.Items),
	}

	// Convert metadata to map for snapshotkv
	metaMap := map[string]any{
		"id":         meta.ID,
		"created_at": meta.CreatedAt,
		"metadata":   meta.Metadata,
		"item_count": meta.ItemCount,
	}

	if s.ttl > 0 {
		return s.db.SetWithBlobEx(key, metaMap, itemsData, s.ttl)
	}

	return s.db.SetWithBlob(key, metaMap, itemsData)
}

func (s *SnapshotConversationStorage) Get(ctx context.Context, id string) (*StoredConversation, error) {
	key := s.conversationKey(id)

	metaMap, err := s.db.Get(key)
	if err != nil {
		if err == snapshotkv.ErrNotFound {
			return nil, fmt.Errorf("conversation not found")
		}
		return nil, err
	}

	// Parse metadata
	meta := &conversationMetadata{}
	if v, ok := metaMap["id"].(string); ok {
		meta.ID = v
	}
	if v, ok := metaMap["created_at"].(time.Time); ok {
		meta.CreatedAt = v
	} else if v, ok := metaMap["created_at"].(string); ok {
		meta.CreatedAt, _ = time.Parse(time.RFC3339, v)
	}
	if v, ok := metaMap["metadata"].(map[string]interface{}); ok {
		meta.Metadata = v
	}
	if v, ok := metaMap["item_count"].(int); ok {
		meta.ItemCount = v
	}

	// Get items from blob
	itemsData, err := s.db.GetBlob(key)
	if err != nil && err != snapshotkv.ErrNoBlob {
		return nil, fmt.Errorf("failed to get conversation items: %w", err)
	}

	var items []openai.ConversationItem
	if len(itemsData) > 0 {
		if err := json.Unmarshal(itemsData, &items); err != nil {
			return nil, fmt.Errorf("failed to unmarshal conversation items: %w", err)
		}
	}

	return &StoredConversation{
		ID:        meta.ID,
		CreatedAt: meta.CreatedAt,
		Metadata:  meta.Metadata,
		Items:     items,
	}, nil
}

func (s *SnapshotConversationStorage) Delete(ctx context.Context, id string) error {
	key := s.conversationKey(id)
	return s.db.Delete(key)
}

func (s *SnapshotConversationStorage) Update(ctx context.Context, id string, metadata map[string]interface{}) error {
	conversation, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	conversation.Metadata = metadata
	return s.Store(ctx, conversation)
}

func (s *SnapshotConversationStorage) AddItems(ctx context.Context, conversationID string, items []openai.ConversationItem) error {
	conversation, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}

	conversation.Items = append(conversation.Items, items...)
	return s.Store(ctx, conversation)
}

func (s *SnapshotConversationStorage) GetItems(ctx context.Context, conversationID string, after string, limit int, order string) ([]openai.ConversationItem, bool, error) {
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

func (s *SnapshotConversationStorage) GetItem(ctx context.Context, conversationID string, itemID string) (*openai.ConversationItem, error) {
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

func (s *SnapshotConversationStorage) DeleteItem(ctx context.Context, conversationID string, itemID string) error {
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

// ListConversations returns all conversation IDs (for admin/debug purposes)
func (s *SnapshotConversationStorage) ListConversations() []string {
	keys := s.db.FindKeysByPrefix(conversationKeyPrefix)
	// Strip the prefix from keys
	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = strings.TrimPrefix(key, conversationKeyPrefix)
	}
	return result
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

// ListConversations returns all conversation IDs (for admin/debug purposes)
func (s *MemoryConversationStorage) ListConversations() []string {
	keys := make([]string, 0, len(s.conversations))
	for id := range s.conversations {
		keys = append(keys, id)
	}
	return keys
}
