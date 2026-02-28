package router

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/paularlott/llmrouter/internal/conversations"
	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/logger"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
)

type Logger = logger.Logger

type Provider struct {
	Name              string
	ProviderType      string // openai | claude | gemini | ollama | mistral | zai
	Client            ai.Client
	Enabled           bool
	Healthy           bool
	ActiveCompletions atomic.Int64
	Fetching          atomic.Bool
	StaticModels      bool
	ModelWhitelist    []string
	Weight            float64
	Tags              []string            // provider-level tags
	ModelTags         map[string][]string // model_id -> tags
}

type Router struct {
	Providers            map[string]*Provider
	ModelMap             map[string][]string
	ModelMapMu           sync.RWMutex
	ModelTags            map[string][]string // model_id -> merged tags across all providers
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
	smartRouter          *SmartRouter
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
)
