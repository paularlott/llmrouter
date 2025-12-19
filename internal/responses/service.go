package responses

import (
	"context"
	"fmt"
	"time"

	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/openai"
)

type Service struct {
	storage storage.ResponseStorage
	config  *types.ResponsesConfig
	router  ChatCompletionRouter
}

// ChatCompletionRouter interface for processing chat completions
type ChatCompletionRouter interface {
	CreateChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error)
}

func NewService(config *types.ResponsesConfig, router ChatCompletionRouter) (*Service, error) {
	var store storage.ResponseStorage
	var err error

	if config.StoragePath == "" {
		// Use memory storage when no storage path specified
		store = storage.NewMemoryStorage()
	} else {
		storagePath := config.StoragePath

		ttl := time.Duration(config.TTLDays) * 24 * time.Hour
		if config.TTLDays == 0 {
			ttl = 30 * 24 * time.Hour // Default 30 days
		}

		store, err = storage.NewBadgerStorage(storagePath, ttl)
		if err != nil {
			return nil, fmt.Errorf("failed to create badger storage: %w", err)
		}
	}

	return &Service{
		storage: store,
		config:  config,
		router:  router,
	}, nil
}

func (s *Service) CreateResponse(ctx context.Context, req *openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	responseID := storage.GenerateResponseID()
	now := time.Now()

	storedResponse := &storage.StoredResponse{
		ID:        responseID,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    storage.StatusPending,
		Request: map[string]interface{}{
			"model":    req.Model,
			"messages": req.Messages,
			"metadata": req.Metadata,
		},
		Response: map[string]interface{}{},
		Metadata: storage.ResponseMetadata{
			Model:     req.Model,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	if err := s.storage.Store(ctx, storedResponse); err != nil {
		return nil, fmt.Errorf("failed to store response: %w", err)
	}

	// Process the response asynchronously
	go s.processResponse(context.Background(), responseID, req)

	return &openai.ResponseObject{
		ID:      responseID,
		Object:  "response",
		Created: now.Unix(),
		Model:   req.Model,
		Status:  string(storage.StatusPending),
	}, nil
}

func (s *Service) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	stored, err := s.storage.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	response := &openai.ResponseObject{
		ID:      stored.ID,
		Object:  "response",
		Created: stored.CreatedAt.Unix(),
		Model:   stored.Metadata.Model,
		Status:  string(stored.Status),
	}

	// Add output if response is completed
	if stored.Status == storage.StatusCompleted {
		if output, ok := stored.Response["output"]; ok {
			response.Output = []any{output}
		}
	}

	// Add usage if available
	if usage, ok := stored.Response["usage"]; ok {
		if usageMap, ok := usage.(map[string]interface{}); ok {
			response.Usage = &openai.Usage{
				PromptTokens:     int(usageMap["prompt_tokens"].(float64)),
				CompletionTokens: int(usageMap["completion_tokens"].(float64)),
				TotalTokens:      int(usageMap["total_tokens"].(float64)),
			}
		}
	}

	return response, nil
}

func (s *Service) ListResponses(ctx context.Context, filter storage.ResponseFilter) (*openai.ResponseListResponse, error) {
	stored, err := s.storage.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	responses := make([]openai.ResponseObject, len(stored))
	for i, sr := range stored {
		responses[i] = openai.ResponseObject{
			ID:      sr.ID,
			Object:  "response",
			Created: sr.CreatedAt.Unix(),
			Model:   sr.Metadata.Model,
			Status:  string(sr.Status),
		}
	}

	return &openai.ResponseListResponse{
		Object: "list",
		Data:   responses,
	}, nil
}

func (s *Service) DeleteResponse(ctx context.Context, id string) error {
	return s.storage.Delete(ctx, id)
}

func (s *Service) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	if err := s.storage.UpdateStatus(ctx, id, storage.StatusCancelled); err != nil {
		return nil, err
	}

	return s.GetResponse(ctx, id)
}

func (s *Service) CompactResponses(ctx context.Context) error {
	return s.storage.RunGC()
}

func (s *Service) Close() error {
	return s.storage.Close()
}

// StoreCompletionResponse stores a completed chat completion response
func (s *Service) StoreCompletionResponse(ctx context.Context, responseID string, chatResp *openai.ChatCompletionResponse, provider string) error {
	stored, err := s.storage.Get(ctx, responseID)
	if err != nil {
		return err
	}

	stored.Status = storage.StatusCompleted
	stored.UpdatedAt = time.Now()
	stored.Response = map[string]interface{}{
		"output": chatResp,
		"usage":  chatResp.Usage,
	}
	stored.Metadata.Provider = provider
	stored.Metadata.UpdatedAt = stored.UpdatedAt

	return s.storage.Store(ctx, stored)
}

// processResponse processes a stored response through the LLM
func (s *Service) processResponse(ctx context.Context, responseID string, req *openai.CreateResponseRequest) {
	// Update status to in_progress
	if err := s.storage.UpdateStatus(ctx, responseID, storage.StatusInProgress); err != nil {
		return
	}

	// Build conversation messages
	messages := req.Messages

	// If previous_response_id is provided, load conversation history
	if req.PreviousResponseID != "" {
		prevResponse, err := s.storage.Get(ctx, req.PreviousResponseID)
		if err != nil {
			// Update status to error
			s.storage.UpdateStatus(ctx, responseID, storage.StatusError)
			return
		}

		// Extract messages from previous response's request
		if prevReq, ok := prevResponse.Request["messages"]; ok {
			if prevMessages, ok := prevReq.([]interface{}); ok {
				// Convert stored messages back to openai.Message format
				for _, msg := range prevMessages {
					if msgMap, ok := msg.(map[string]interface{}); ok {
						role, _ := msgMap["role"].(string)
						content, _ := msgMap["content"].(string)
						messages = append([]openai.Message{{Role: role, Content: content}}, messages...)
					}
				}
			}
		}

		// Also include the assistant's response from the previous interaction
		if prevResp, ok := prevResponse.Response["output"]; ok {
			if outputs, ok := prevResp.([]interface{}); ok && len(outputs) > 0 {
				if output, ok := outputs[0].(map[string]interface{}); ok {
					if choices, ok := output["choices"].([]interface{}); ok && len(choices) > 0 {
						if choice, ok := choices[0].(map[string]interface{}); ok {
							if message, ok := choice["message"].(map[string]interface{}); ok {
								role, _ := message["role"].(string)
								content, _ := message["content"].(string)
								messages = append(messages, openai.Message{Role: role, Content: content})
							}
						}
					}
				}
			}
		}
	}

	// Convert to chat completion request
	chatReq := &openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    req.Tools,
	}

	// Process through router
	chatResp, err := s.router.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		// Update status to error
		s.storage.UpdateStatus(ctx, responseID, storage.StatusError)
		return
	}

	// Store the completed response
	stored, err := s.storage.Get(ctx, responseID)
	if err != nil {
		return
	}

	stored.Status = storage.StatusCompleted
	stored.UpdatedAt = time.Now()
	stored.Response = map[string]interface{}{
		"output": chatResp,
		"usage":  chatResp.Usage,
	}
	stored.Metadata.UpdatedAt = stored.UpdatedAt

	s.storage.Store(ctx, stored)
}
