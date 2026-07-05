package router

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/paularlott/llmrouter/internal/admin"
	"github.com/paularlott/llmrouter/internal/conversations"
	"github.com/paularlott/llmrouter/internal/responses"
	"github.com/paularlott/llmrouter/internal/storage"
	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/logger"
	"github.com/paularlott/mcp/ai"
	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/webchat"
)

type Logger = logger.Logger

type Provider struct {
	Name              string
	ProviderType      string // openai | claude | gemini | ollama | mistral | zai
	Client            ai.Client
	Enabled           bool
	Healthy           atomic.Bool
	ActiveCompletions atomic.Int64
	Fetching          atomic.Bool
	Models            []string // static model list; if set, overrides provider API discovery
	ModelAllowlist    []string
	ModelDenylist     []string
	Weight            float64
	Tags              []string            // provider-level tags
	ModelTags         map[string][]string // model_id -> tags
	ModelAliases      map[string]string   // alias -> real model name
}

type Router struct {
	Providers            map[string]*Provider
	ModelMap             map[string][]string
	ModelMapMu           sync.RWMutex
	ModelTags            map[string][]string // model_id -> merged tags across all providers
	config               *types.Config
	logger               Logger
	traceEnabled         bool
	shutdownChan         chan struct{}
	shutdownOnce         sync.Once
	wg                   sync.WaitGroup
	mcpServer            *MCPServer
	mux                  *http.ServeMux
	sharedStore          *storage.Store
	responsesService     *responses.Service
	conversationsService *conversations.Service
	smartRouter          *SmartRouter
	admin                *admin.Admin
	chatServer           *webchat.Server
	mcpStorage           storage.MCPStorage
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
