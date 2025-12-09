# LLM Router

A simple router for LLM services that supports the OpenAI protocol. It aggregates models from multiple OpenAI-compatible servers and routes chat completion requests based on model availability and load.

## Features

- **OpenAI Protocol Compatible**: Supports the standard OpenAI API endpoints
- **Multiple Provider Support**: Configure multiple OpenAI-compatible servers
- **Model Aggregation**: Automatically aggregates available models from all providers
- **Load-Based Routing**: Routes to the provider with the least active completions when a model is available on multiple servers
- **Transparent Proxying**: Passes through all OpenAI protocol features including tool calls without server-side interpretation
- **Token Usage Estimation**: Estimates token usage when not provided by the upstream service
- **Configuration via TOML**: Simple TOML configuration file
- **Structured Logging**: Uses slog for structured logging

## Configuration

Create a `config.toml` file:

```toml
[server]
port = 12345
host = "0.0.0.0"

[logging]
level = "info"
format = "console"

[[providers]]
name = "openai1"
base_url = "https://api.openai.com/v1"
token = "your-api-key-here"
enabled = true

[[providers]]
name = "openai2"
base_url = "https://api.openai.com/v1"
token = "another-api-key"
enabled = true

# Add more providers as needed
[[providers]]
name = "local-llm"
base_url = "http://localhost:8080/v1"
token = ""
enabled = true

# Provider with static models (no model fetching)
[[providers]]
name = "google"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
token = "your-google-key"
enabled = true
models = ["gemini-2.5-flash-lite", "gemini-2.5-pro"]

# Provider with allowlist (only these models will be available)
[[providers]]
name = "openai-filtered"
base_url = "https://api.openai.com/v1"
token = "your-api-key"
enabled = true
allowlist = ["gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"]

# Provider with denylist (these models will be excluded)
[[providers]]
name = "azure-openai"
base_url = "https://your-resource.openai.azure.com"
token = "your-azure-key"
enabled = true
denylist = ["text-davinci-003", "text-curie-001"]
```

## Static Models

You can optionally specify a list of available models for a provider instead of fetching them dynamically:

```toml
[[providers]]
name = "google"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
token = "your-google-key"
enabled = true
models = ["gemini-1.5-flash", "gemini-1.5-pro", "gemini-pro"]
```

**Benefits of Static Models:**
- **Faster startup**: No need to query provider API for available models
- **Reliability**: Works even when provider's API is temporarily unavailable
- **Privacy**: No need to expose model list to provider
- **Predictable behavior**: Always use the same predefined model list

**Static Model Behavior:**
- Router uses the provided model list instead of fetching from provider
- No reconnection attempts if provider fails (models remain available)
- Perfect for providers with fixed model catalogs or private deployments

## Model Filtering (Allowlist/Denylist)

You can control which models from each provider are exposed to clients using allowlist and denylist filters:

### Allowlist
Only models explicitly listed in the allowlist will be available from the provider:

```toml
[[providers]]
name = "openai-enterprise"
base_url = "https://api.openai.com/v1"
token = "your-api-key"
enabled = true
allowlist = ["gpt-4", "gpt-4-turbo-preview", "gpt-3.5-turbo"]
```

### Denylist
All models from the provider will be available except those listed in the denylist:

```toml
[[providers]]
name = "azure-openai"
base_url = "https://your-resource.openai.azure.com"
token = "your-azure-key"
enabled = true
denylist = ["text-davinci-003", "text-curie-001", "text-ada-001"]
```

### Combining with Static Models
You can use allowlist/denylist with static models for precise control:

```toml
[[providers]]
name = "google"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
token = "your-google-key"
enabled = true
models = ["gemini-2.0-flash-exp", "gemini-2.0-flash-thinking-exp", "gemini-1.5-pro", "gemini-1.5-flash"]
allowlist = ["gemini-2.0-flash-exp", "gemini-1.5-pro"]  # Only expose these two
```

**Filtering Rules:**
- Denylist is applied first - any model in the denylist is always excluded
- If allowlist is provided, only models in the allowlist are included
- If no allowlist is provided, all non-denylisted models are included
- Filtering works with both dynamic and static model lists

## Usage

### Running the Router

```bash
# Build the router
go build -o llmrouter .

# Run with default config
./llmrouter

# Run with custom config
./llmrouter -config /path/to/config.toml

# Override port
./llmrouter -port 8080

# Set log level
./llmrouter -log-level debug

# Use JSON logging format
./llmrouter -log-format json
```

### API Endpoints

The router exposes the following endpoints:

#### GET /v1/models
Returns the aggregated list of models from all enabled providers.

```bash
curl http://localhost:12345/v1/models
```

#### POST /v1/chat/completions
Creates a chat completion. The request is routed to the appropriate provider based on the selected model.

```bash
curl -X POST http://localhost:12345/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ],
    "max_tokens": 100
  }'
```

#### GET /health
Returns health information including provider status.

```bash
curl http://localhost:12345/health
```

## Routing Logic

1. **Model Selection**: When a chat completion request comes in, the router checks which providers have the requested model
2. **Load Balancing**: If the model is available on multiple providers, the request is routed to the provider with the least active completions
3. **Failover**: If a provider doesn't have the requested model, the router returns a 404 error

## Token Estimation

The router includes token usage estimation based on the OpenAI token counting logic from `/Users/paul/Code/Source/mcp/openai/tokens.go`. If the upstream provider doesn't return token usage information, the router estimates it using:
- Word boundaries and punctuation for basic text
- Overhead for chat message structure (roles, special tokens)
- Tool call arguments estimation

## Logging

The router uses structured logging with configurable levels:
- `trace` - Most verbose
- `debug` - Debug information
- `info` - General information (default)
- `warn` - Warning messages
- `error` - Error messages

Log formats:
- `console` - Human-readable format (default)
- `json` - JSON format for machine processing

## Command Line Options

```
-config string
    Configuration file path (default "config.toml")
-log-level string
    Log level: trace, debug, info, warn, error
-log-format string
    Log format: console, json
-port int
    Port to listen on (overrides config)
```

## Example Use Cases

1. **Load Balancing**: Balance requests across multiple API keys for the same service
2. **Provider Failover**: Route to different providers if one is unavailable
3. **Local + Cloud**: Use both local LLM servers and cloud-based APIs
4. **Model Gateway**: Provide a single endpoint that aggregates multiple LLM providers