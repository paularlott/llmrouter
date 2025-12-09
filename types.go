package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/paularlott/logger"
	"github.com/paularlott/mcp/openai"
)

// Use the logger interface from the logger package
type Logger = logger.Logger

// Router specific types
type Provider struct {
	Name         string
	BaseURL      string
	Token        string
	Enabled      bool
	Healthy      bool
	Client       OpenAIClient
	ActiveCompletions int64
	StaticModels bool   // true if models list is static (from config)
	Whitelist    []string // allowed models from this provider
	Blacklist    []string // blocked models from this provider
}

type Router struct {
	Providers    map[string]*Provider
	ModelMap     map[string][]string // model -> provider names
	ModelMapMu   sync.RWMutex       // protects ModelMap
	config       *Config
	logger       Logger
	shutdownChan chan struct{}     // for background task
	wg           sync.WaitGroup    // for background task cleanup
}

// OpenAI client interface
type OpenAIClient interface {
	ListModels(ctx context.Context) (*openai.ModelsResponse, error)
	ListModelsWithTimeout(ctx context.Context) (*openai.ModelsResponse, error)
	CreateChatCompletion(ctx context.Context, req *openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error)
	CreateChatCompletionRaw(ctx context.Context, req *openai.ChatCompletionRequest) (*http.Response, error)
}

// Type aliases for OpenAI types
type (
	ModelsResponse           = openai.ModelsResponse
	Model                    = openai.Model
	ChatCompletionRequest    = openai.ChatCompletionRequest
	ChatCompletionResponse   = openai.ChatCompletionResponse
	Message                  = openai.Message
	Choice                   = openai.Choice
	Usage                    = openai.Usage
	ToolCall                 = openai.ToolCall
	ToolCallFunction         = openai.ToolCallFunction
	PromptTokensDetails      = openai.PromptTokensDetails
	CompletionTokensDetails  = openai.CompletionTokensDetails
)