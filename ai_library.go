package main

import (
	"context"

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