package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/logger"
	"github.com/paularlott/mcp/openai"
)

// Use the logger interface from the logger package
type Logger = logger.Logger

// Router specific types
type Provider struct {
	Name              string
	BaseURL           string
	Token             string
	Enabled           bool
	Healthy           bool
	Client            OpenAIClient
	ActiveCompletions int64
	StaticModels      bool     // true if models list is static (from config)
	Allowlist         []string // allowed models from this provider
	Denylist          []string // blocked models from this provider
	NativeResponses   bool     // true if provider supports native responses API
}

// GetNativeResponses returns whether the provider supports native responses API
func (p *Provider) GetNativeResponses() bool {
	return p.NativeResponses
}

type Router struct {
	Providers       map[string]*Provider
	ModelMap        map[string][]string // model -> provider names
	ModelMapMu      sync.RWMutex        // protects ModelMap
	config          *Config
	logger          Logger
	shutdownChan    chan struct{}  // for background task
	shutdownOnce    sync.Once      // ensures shutdown is only called once
	wg              sync.WaitGroup // for background task cleanup
	mcpServer       *MCPServer     // MCP server instance
	mux             *http.ServeMux
	responsesService *responses.Service // responses service instance
}

// OpenAI client interface
type OpenAIClient interface {
	ListModels(ctx context.Context) (*openai.ModelsResponse, error)
	ListModelsWithTimeout(ctx context.Context) (*openai.ModelsResponse, error)
	CreateChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error)
	CreateChatCompletionRaw(ctx context.Context, req *openai.ChatCompletionRequest) (*http.Response, error)
	CreateEmbedding(ctx context.Context, req *openai.EmbeddingRequest) (*openai.EmbeddingResponse, error)
}

// Type aliases for OpenAI types
type (
	ModelsResponse          = openai.ModelsResponse
	Model                   = openai.Model
	ChatCompletionRequest   = openai.ChatCompletionRequest
	ChatCompletionResponse  = openai.ChatCompletionResponse
	Message                 = openai.Message
	Choice                  = openai.Choice
	Delta                   = openai.Delta
	Usage                   = openai.Usage
	Tool                    = openai.Tool
	ToolFunction            = openai.ToolFunction
	ToolCall                = openai.ToolCall
	ToolCallFunction        = openai.ToolCallFunction
	PromptTokensDetails     = openai.PromptTokensDetails
	CompletionTokensDetails = openai.CompletionTokensDetails
	EmbeddingRequest        = openai.EmbeddingRequest
	EmbeddingResponse       = openai.EmbeddingResponse
	Embedding               = openai.Embedding
	// Responses types
	ResponseObject        = openai.ResponseObject
	ResponseListResponse  = openai.ResponseListResponse
	CreateResponseRequest = openai.CreateResponseRequest
	ResponseFilter        = storage.ResponseFilter
)
