package conversations

import (
	"context"
	"fmt"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/openai"
)

type Service struct {
	storage storage.ConversationStorage
	config  *types.ConversationsConfig
}

func NewService(config *types.ConversationsConfig) (*Service, error) {
	var store storage.ConversationStorage
	var err error

	if config.StoragePath == "" {
		// Use memory storage when no storage path specified
		store = storage.NewMemoryConversationStorage()
	} else {
		storagePath := config.StoragePath

		ttl := time.Duration(config.TTLDays) * 24 * time.Hour
		if config.TTLDays == 0 {
			ttl = 30 * 24 * time.Hour // Default 30 days
		}

		store, err = storage.NewBadgerConversationStorage(storagePath, ttl)
		if err != nil {
			return nil, fmt.Errorf("failed to create badger storage: %w", err)
		}
	}

	return &Service{
		storage: store,
		config:  config,
	}, nil
}

func (s *Service) CreateConversation(ctx context.Context, req *openai.CreateConversationRequest) (*openai.Conversation, error) {
	conversationID := storage.GenerateConversationID()
	now := time.Now()

	// Initialize items with IDs and status
	items := make([]openai.ConversationItem, len(req.Items))
	for i, item := range req.Items {
		if item.ID == "" {
			item.ID = storage.GenerateMessageID()
		}
		if item.Status == "" {
			item.Status = "completed"
		}
		items[i] = item
	}

	storedConversation := &storage.StoredConversation{
		ID:        conversationID,
		CreatedAt: now,
		Metadata:  req.Metadata,
		Items:     items,
	}

	if err := s.storage.Store(ctx, storedConversation); err != nil {
		return nil, fmt.Errorf("failed to store conversation: %w", err)
	}

	return &openai.Conversation{
		ID:        storedConversation.ID,
		Object:    "conversation",
		CreatedAt: storedConversation.CreatedAt.Unix(),
		Metadata:  storedConversation.Metadata,
	}, nil
}

func (s *Service) GetConversation(ctx context.Context, id string) (*openai.Conversation, error) {
	stored, err := s.storage.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	return &openai.Conversation{
		ID:        stored.ID,
		Object:    "conversation",
		CreatedAt: stored.CreatedAt.Unix(),
		Metadata:  stored.Metadata,
	}, nil
}

func (s *Service) UpdateConversation(ctx context.Context, id string, req *openai.UpdateConversationRequest) (*openai.Conversation, error) {
	if err := s.storage.Update(ctx, id, req.Metadata); err != nil {
		return nil, err
	}
	return s.GetConversation(ctx, id)
}

func (s *Service) DeleteConversation(ctx context.Context, id string) (*openai.ConversationDeleteResponse, error) {
	// Check if conversation exists
	_, err := s.storage.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.storage.Delete(ctx, id); err != nil {
		return nil, err
	}

	return &openai.ConversationDeleteResponse{
		ID:      id,
		Object:  "conversation.deleted",
		Deleted: true,
	}, nil
}

func (s *Service) ListItems(ctx context.Context, conversationID string, after string, limit int, order string, include []string) (*openai.ConversationItemListResponse, error) {
	items, hasMore, err := s.storage.GetItems(ctx, conversationID, after, limit, order)
	if err != nil {
		return nil, err
	}

	// TODO: Handle include options to add additional data
	// For now, return items as-is

	response := &openai.ConversationItemListResponse{
		Object:  "list",
		Data:    items,
		HasMore: hasMore,
	}

	if len(items) > 0 {
		response.FirstID = items[0].ID
		response.LastID = items[len(items)-1].ID
	}

	return response, nil
}

func (s *Service) CreateItems(ctx context.Context, conversationID string, req *openai.CreateItemsRequest, include []string) (*openai.ConversationItemListResponse, error) {
	// Validate conversation exists
	_, err := s.storage.Get(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	// Initialize items with IDs and status
	items := make([]openai.ConversationItem, len(req.Items))
	for i, item := range req.Items {
		if item.ID == "" {
			item.ID = storage.GenerateMessageID()
		}
		if item.Status == "" {
			item.Status = "completed"
		}
		items[i] = item
	}

	if err := s.storage.AddItems(ctx, conversationID, items); err != nil {
		return nil, fmt.Errorf("failed to add items: %w", err)
	}

	// Return the created items
	response := &openai.ConversationItemListResponse{
		Object:  "list",
		Data:    items,
		HasMore: false,
	}

	if len(items) > 0 {
		response.FirstID = items[0].ID
		response.LastID = items[len(items)-1].ID
	}

	return response, nil
}

func (s *Service) GetItem(ctx context.Context, conversationID string, itemID string, include []string) (*openai.ConversationItem, error) {
	item, err := s.storage.GetItem(ctx, conversationID, itemID)
	if err != nil {
		return nil, err
	}

	// TODO: Handle include options

	return item, nil
}

func (s *Service) DeleteItem(ctx context.Context, conversationID string, itemID string) (*openai.Conversation, error) {
	if err := s.storage.DeleteItem(ctx, conversationID, itemID); err != nil {
		return nil, err
	}

	// Return the updated conversation
	return s.GetConversation(ctx, conversationID)
}

func (s *Service) Close() {
	if s.storage != nil {
		s.storage.Close()
	}
}
