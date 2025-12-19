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

// CompletionFunc is a function that creates a chat completion
type CompletionFunc func(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error)

func (s *Service) CreateResponse(ctx context.Context, req *openai.CreateResponseRequest, completionFunc CompletionFunc) (*openai.ResponseObject, error) {
	// Check if the model's provider supports native responses
	if providerName, err := s.getProviderForModel(req.Model); err == nil {
		if provider := s.getProvider(providerName); provider != nil && provider.GetNativeResponses() {
			// Use native responses API - delegate to provider
			return s.createNativeResponse(ctx, req, provider)
		}
	}
	
	// Use emulated responses (existing logic)
	return s.createEmulatedResponse(ctx, req, completionFunc)
}

// createEmulatedResponse handles the existing emulation logic
func (s *Service) createEmulatedResponse(ctx context.Context, req *openai.CreateResponseRequest, completionFunc CompletionFunc) (*openai.ResponseObject, error) {
	responseID := storage.GenerateResponseID()
	now := time.Now()

	storedResponse := &storage.StoredResponse{
		ID:        responseID,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    storage.StatusPending,
		Request: map[string]interface{}{
			"model":        req.Model,
			"input":        req.Input,
			"instructions": req.Instructions,
			"modalities":   req.Modalities,
			"tools":        req.Tools,
			"metadata":     req.Metadata,
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
	go s.processResponse(context.Background(), responseID, req, completionFunc)

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
func (s *Service) processResponse(ctx context.Context, responseID string, req *openai.CreateResponseRequest, completionFunc CompletionFunc) {
	// Update status to in_progress
	if err := s.storage.UpdateStatus(ctx, responseID, storage.StatusInProgress); err != nil {
		return
	}

	// Convert input and instructions to messages
	var messages []openai.Message
	
	// Load previous conversation if previous_response_id provided
	if req.PreviousResponseID != "" {
		prevResponse, err := s.storage.Get(ctx, req.PreviousResponseID)
		if err == nil {
			// Extract previous input as user message
			if prevInput, ok := prevResponse.Request["input"].([]interface{}); ok {
				for _, inp := range prevInput {
					if inputStr, ok := inp.(string); ok {
						messages = append(messages, openai.Message{
							Role:    "user",
							Content: inputStr,
						})
					}
				}
			}
			// Extract previous output as assistant message
			if prevOutput, ok := prevResponse.Response["output"]; ok {
				if chatResp, ok := prevOutput.(*openai.ChatCompletionResponse); ok {
					if len(chatResp.Choices) > 0 {
						messages = append(messages, openai.Message{
							Role:    chatResp.Choices[0].Message.Role,
							Content: chatResp.Choices[0].Message.GetContentAsString(),
						})
					}
				}
			}
		}
	}
	
	// Add instructions as system message if provided
	if req.Instructions != "" {
		messages = append(messages, openai.Message{
			Role:    "system",
			Content: req.Instructions,
		})
	}
	
	// Convert input to user messages
	for _, input := range req.Input {
		if inputStr, ok := input.(string); ok {
			messages = append(messages, openai.Message{
				Role:    "user",
				Content: inputStr,
			})
		}
	}

	// Convert to chat completion request
	chatReq := &openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    req.Tools,
	}

	// Process through the provided completion function or fallback to router
	var chatResp *openai.ChatCompletionResponse
	var err error
	if completionFunc != nil {
		chatResp, err = completionFunc(ctx, chatReq)
	} else {
		chatResp, err = s.router.CreateChatCompletion(ctx, chatReq)
	}
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

// Helper methods for provider access
func (s *Service) getProviderForModel(model string) (string, error) {
	if router, ok := s.router.(interface{ GetProviderForModel(string) (string, error) }); ok {
		return router.GetProviderForModel(model)
	}
	return "", fmt.Errorf("router does not support GetProviderForModel")
}

type ProviderInterface interface {
	GetNativeResponses() bool
}

func (s *Service) getProvider(name string) ProviderInterface {
	if router, ok := s.router.(interface{ GetProvider(string) interface{ GetNativeResponses() bool } }); ok {
		return router.GetProvider(name)
	}
	return nil
}

// createNativeResponse delegates to provider's native responses API
func (s *Service) createNativeResponse(ctx context.Context, req *openai.CreateResponseRequest, provider ProviderInterface) (*openai.ResponseObject, error) {
	// TODO: Implement native provider delegation
	// For now, fallback to emulation
	return s.createEmulatedResponse(ctx, req, nil)
}
