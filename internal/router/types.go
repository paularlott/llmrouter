package router

import (
	"context"
	"net/http"
	"sync"

	"github.com/paularlott/llmrouter/internal/conversations"
	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/logger"
	"github.com/paularlott/mcp/ai/openai"
)

// Logger is the logger interface
type Logger = logger.Logger

type Provider struct {
	Name              string
	BaseURL           string
	Token             string
	Enabled           bool
	Healthy           bool
	Client            OpenAIClient
	ActiveCompletions int64
	StaticModels      bool
	Allowlist         []string
	Denylist          []string
	NativeResponses   bool
}

func (p *Provider) GetNativeResponses() bool {
	return p.NativeResponses
}

type Router struct {
	Providers            map[string]*Provider
	ModelMap             map[string][]string
	ModelMapMu           sync.RWMutex
	config               *types.Config
	logger               Logger
	shutdownChan         chan struct{}
	shutdownOnce         sync.Once
	wg                   sync.WaitGroup
	mcpServer            *MCPServer
	mux                  *http.ServeMux
	sharedStore          *storage.Store
	responsesService     *responses.Service
	conversationsService *conversations.Service
}

type OpenAIClient interface {
	ListModels(ctx context.Context) (*openai.ModelsResponse, error)
	ListModelsWithTimeout(ctx context.Context) (*openai.ModelsResponse, error)
	CreateChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error)
	CreateChatCompletionRaw(ctx context.Context, req *openai.ChatCompletionRequest) (*http.Response, error)
	CreateEmbedding(ctx context.Context, req *openai.EmbeddingRequest) (*openai.EmbeddingResponse, error)
}

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
	ResponseObject          = openai.ResponseObject
	ResponseListResponse    = openai.ResponseListResponse
	CreateResponseRequest   = openai.CreateResponseRequest
	ResponseFilter          = storage.ResponseFilter
)
