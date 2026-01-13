package main

import (
	"context"
	"fmt"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/openai"
	"github.com/paularlott/scriptling/object"
)

// RouterInterface defines the interface needed by AILibrary
type RouterInterface interface {
	CreateChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
	CreateEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

// AILibrary provides AI completion and tool calling capabilities
type AILibrary struct {
	router *Router
}

// NewAILibrary creates a new AI library instance
func NewAILibrary(router *Router) *AILibrary {
	return &AILibrary{
		router: router,
	}
}

// GetLibrary returns the scriptling library object for AI operations
func (ai *AILibrary) GetLibrary() *object.Library {
	return object.NewLibraryBuilder("ai", "AI completion and tool calling capabilities").
		FunctionWithHelp("completion", func(model string, messages []map[string]string) (string, error) {
			// Convert messages to our format
			var msgs []Message
			for _, msg := range messages {
				msgs = append(msgs, Message{
					Role:    msg["role"],
					Content: msg["content"],
				})
			}

			req := &ChatCompletionRequest{
				Model:    model,
				Messages: msgs,
			}

			// Get completion with automatic tool calling
			resp, err := ai.CreateChatCompletionWithTools(context.Background(), req)
			if err != nil {
				return "", err
			}

			// Return the response as a string
			if len(resp.Choices) > 0 {
				msg := &resp.Choices[0].Message
				if content := msg.GetContentAsString(); content != "" {
					return content, nil
				}
			}

			return "", nil
		}, "completion(model, messages) - Create a chat completion with automatic tool calling").
		FunctionWithHelp("embedding", func(model string, input interface{}) ([][]float64, error) {
			req := &EmbeddingRequest{
				Model: model,
				Input: input,
			}

			resp, err := ai.router.CreateEmbedding(context.Background(), req)
			if err != nil {
				return nil, err
			}

			// Convert embeddings to Go slice
			embeddings := make([][]float64, len(resp.Data))
			for i, emb := range resp.Data {
				embeddings[i] = emb.Embedding
			}

			return embeddings, nil
		}, "embedding(model, input) - Create embeddings for text input").
		FunctionWithHelp("response_create", func(model string, input interface{}, instructions ...string) (string, error) {
			// Check if responses service is available
			if ai.router.responsesService == nil {
				return "", fmt.Errorf("responses service not available")
			}

			// Build the request
			req := &openai.CreateResponseRequest{
				Model: model,
			}

			// Convert input to []any
			switch v := input.(type) {
			case string:
				req.Input = []any{v}
			case []any:
				req.Input = v
			default:
				req.Input = []any{fmt.Sprintf("%v", input)}
			}

			// Add instructions if provided
			if len(instructions) > 0 {
				req.Instructions = instructions[0]
			}

			// Create the response
			resp, err := ai.router.responsesService.CreateResponse(context.Background(), req, nil)
			if err != nil {
				return "", err
			}

			return resp.ID, nil
		}, "response_create(model, input, instructions=None) - Create a response for async processing").
		FunctionWithHelp("response_get", func(id string) (map[string]interface{}, error) {
			// Check if responses service is available
			if ai.router.responsesService == nil {
				return nil, fmt.Errorf("responses service not available")
			}

			resp, err := ai.router.responsesService.GetResponse(context.Background(), id)
			if err != nil {
				return nil, err
			}

			// Convert to map for scriptling
			result := map[string]interface{}{
				"id":         resp.ID,
				"object":     resp.Object,
				"created_at": resp.CreatedAt,
				"model":      resp.Model,
				"status":     resp.Status,
			}

			// Add output if available
			if len(resp.Output) > 0 {
				result["output"] = resp.Output
			}

			// Add usage if available
			if resp.Usage != nil {
				result["usage"] = map[string]interface{}{
					"prompt_tokens":     resp.Usage.PromptTokens,
					"completion_tokens": resp.Usage.CompletionTokens,
					"total_tokens":      resp.Usage.TotalTokens,
				}
			}

			// Add error if available
			if resp.Error != nil {
				result["error"] = resp.Error.Message
			}

			return result, nil
		}, "response_get(id) - Get a response by ID").
		FunctionWithHelp("response_delete", func(id string) error {
			// Check if responses service is available
			if ai.router.responsesService == nil {
				return fmt.Errorf("responses service not available")
			}

			return ai.router.responsesService.DeleteResponse(context.Background(), id)
		}, "response_delete(id) - Delete a response by ID").
		FunctionWithHelp("response_cancel", func(id string) (string, error) {
			// Check if responses service is available
			if ai.router.responsesService == nil {
				return "", fmt.Errorf("responses service not available")
			}

			resp, err := ai.router.responsesService.CancelResponse(context.Background(), id)
			if err != nil {
				return "", err
			}

			return resp.Status, nil
		}, "response_cancel(id) - Cancel an in-progress response").
		Build()
}

// CreateChatCompletionWithTools creates a chat completion with automatic tool calling
func (ai *AILibrary) CreateChatCompletionWithTools(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// Convert our types to openai types
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessagesToOpenAI(req.Messages),
		Stream:   req.Stream,
	}

	// Create openai client with MCP server integration
	var mcpServer openai.MCPServer
	if ai.router.mcpServer != nil {
		mcpServer = &openai.MCPServerFuncs{
			ListToolsFunc: func() []mcp.MCPTool {
				return ai.router.mcpServer.server.ListTools()
			},
			CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
				return ai.router.mcpServer.server.CallTool(ctx, name, args)
			},
		}
	}

	// Find a provider to use (use first available)
	var baseURL, apiKey string
	for _, provider := range ai.router.Providers {
		if provider.Enabled && provider.Healthy {
			baseURL = provider.BaseURL
			apiKey = provider.Token
			break
		}
	}

	client, err := openai.New(openai.Config{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		LocalServer: mcpServer,
	})
	if err != nil {
		return nil, err
	}

	// Use the openai client for completion with tool calling
	openaiResp, err := client.ChatCompletion(ctx, openaiReq)
	if err != nil {
		return nil, err
	}

	// Convert back to our types
	return convertOpenAIResponseToOurs(openaiResp), nil
}

// Helper functions to convert between types
func convertMessagesToOpenAI(messages []Message) []openai.Message {
	result := make([]openai.Message, len(messages))
	for i, msg := range messages {
		result[i] = openai.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
	return result
}

func convertOpenAIResponseToOurs(resp *openai.ChatCompletionResponse) *ChatCompletionResponse {
	result := &ChatCompletionResponse{
		ID:      resp.ID,
		Object:  resp.Object,
		Created: resp.Created,
		Model:   resp.Model,
		Choices: make([]Choice, len(resp.Choices)),
	}

	for i, choice := range resp.Choices {
		result.Choices[i] = Choice{
			Index: choice.Index,
			Message: Message{
				Role:    choice.Message.Role,
				Content: choice.Message.GetContentAsString(),
			},
			FinishReason: choice.FinishReason,
		}
	}

	if resp.Usage != nil {
		result.Usage = &Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return result
}