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
	"github.com/paularlott/lmchatkit"
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
	LastServed        atomic.Pointer[lastServed] // last model served + when (for tiebreaks)
}

// lastServed records the most recent request a provider served: the model and
// the time. It drives provider selection tiebreaks — preferring a provider that
// already has the requested model loaded (avoids a cold reload) and, among
// equals, the one idle longest (LRU, so traffic round-robins).
type lastServed struct {
	model string
	at    int64 // unix nano of the provider's last request (any model)
}

// lastServedModel returns the most recent model the provider served, or "" if
// it has served nothing.
func (p *Provider) lastServedModel() string {
	if ls := p.LastServed.Load(); ls != nil {
		return ls.model
	}
	return ""
}

// lastActivityAt returns the time of the provider's last request (any model),
// or 0 if it has never served one. 0 is treated as "idle longest" so a fresh
// provider wins the LRU tiebreak and is warmed before reused hands.
func (p *Provider) lastActivityAt() int64 {
	if ls := p.LastServed.Load(); ls != nil {
		return ls.at
	}
	return 0
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
	chatServer           *lmchatkit.Server
	mcpStorage           storage.MCPStorage
	providerStorage      storage.ProviderStorage
	storedProviderNames  map[string]bool // tracks which providers came from KV storage
	personaStorage       storage.PersonaStorage
	personaSource        *mergedPersonaSource
	eventBroadcaster     *lmchatkit.EventBroadcaster
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
