# LLM Router

A powerful router for LLM services that supports the OpenAI protocol and provides a complete Model Context Protocol (MCP) server with Scriptling integration.

## Features

- **OpenAI Protocol Compatible**: Supports standard OpenAI API endpoints
- **Multiple Provider Support**: Aggregate models from multiple OpenAI-compatible servers
- **Load-Based Routing**: Routes to the provider with the least active completions
- **MCP Server**: Full Model Context Protocol server with tool discovery and execution
- **Scriptling Integration**: Python-like scripting environment for custom tools
- **Dynamic Tool Loading**: Edit tool scripts without restarting the server
- **Automatic Tool Calling**: AI completions automatically execute tools
- **Responses API**: OpenAI-compatible responses storage and retrieval
- **CLI Tools**: Command-line interface for script and tool execution

## Quick Start

```bash
# Build
go build -o llmrouter .

# Or use task/make for multi-platform builds
task              # Build for current platform
make              # Build for current platform
task build-all    # Build for all platforms

# Run the server
./llmrouter server

# Run with custom config
./llmrouter -config /path/to/config.toml server
```

## Configuration

Create a `config.toml` file:

```toml
[server]
port = 12345
host = "0.0.0.0"
token = "your-secret-token"  # Optional: Bearer token for API authentication

[logging]
level = "info"       # trace, debug, info, warn, error
format = "console"   # console, json

# LLM Providers
[[providers]]
name = "local-llm"
base_url = "http://localhost:8080/v1"
token = ""
enabled = true

[[providers]]
name = "openai"
base_url = "https://api.openai.com/v1"
token = "your-api-key"
enabled = true
native_responses = true  # Provider supports native responses API

# Provider with static models (no API fetching)
[[providers]]
name = "google"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai/"
token = "your-google-key"
enabled = true
models = ["gemini-2.5-flash-lite", "gemini-2.5-pro"]

# Provider with allowlist (only these models exposed)
[[providers]]
name = "openai-filtered"
base_url = "https://api.openai.com/v1"
token = "your-api-key"
enabled = true
allowlist = ["gpt-4", "gpt-4-turbo", "gpt-3.5-turbo"]

# Provider with denylist (these models excluded)
[[providers]]
name = "azure"
base_url = "https://your-resource.openai.azure.com"
token = "your-azure-key"
enabled = true
denylist = ["text-davinci-003"]

[mcp]
# Remote MCP servers (optional)
# [[mcp.remote_servers]]
#   namespace = "ai"
#   url = "https://ai.example.com/mcp"
#   token = "your-bearer-token"
#   tool_visibility = "visible"  # visible | hidden | ondemand

[scriptling]
tools_path = "./example-tools"
libraries_path = "./example-libs"

[responses]
storage_path = "./responses.db"
ttl_days = 30
```

### Provider Configuration

| Field | Description |
|-------|-------------|
| `name` | Unique identifier for the provider |
| `base_url` | OpenAI-compatible API base URL |
| `token` | API token/key (optional for local servers) |
| `enabled` | Enable/disable the provider |
| `models` | Static model list (skips API model fetching) |
| `allowlist` | Only expose these models |
| `denylist` | Exclude these models |

### Model Filtering Rules

1. Denylist is applied first - matching models are always excluded
2. If allowlist is provided, only matching models are included
3. If no allowlist, all non-denylisted models are included

### Authentication

Optional bearer token authentication can be enabled by setting the `token` field in the server configuration:

```toml
[server]
token = "your-secret-token"
```

When configured, all API endpoints (except `/health`) require a valid bearer token:

```bash
curl -H "Authorization: Bearer your-secret-token" http://localhost:12345/v1/models
```

If no token is configured, the server runs without authentication.

### MCP Configuration

| Field | Description |
|-------|-------------|
| `namespace` | Namespace for the remote MCP server (prevents tool name conflicts) |
| `url` | URL of the remote MCP server |
| `token` | Optional bearer token for authentication |
| `tool_visibility` | How tools are exposed: `visible` (default), `hidden`, or `ondemand` |

#### Tool Visibility Modes

| Mode | In ListTools | In tool_search | Callable Via |
|------|--------------|----------------|-------------|
| `visible` | ✓ | ✗ | Direct name only |
| `hidden` | ✗ | ✗ | Direct name only |
| `ondemand` | ✗ | ✓ | `execute_tool` only |

**`visible`** (default): Tools appear in the tools/list manifest. LLM can call them directly by name. NOT in `tool_search` (use the manifest instead).

**`hidden`**: Tools are not accessible through any discovery mechanism (not in tools/list, not in tool_search). Can still be called directly by name if you know it. Useful for internal tools that should only be called from scripts with explicit names.

**`ondemand`**: Tools don't appear in the tools/list manifest (reducing initial context sent to LLMs) but are discoverable via `tool_search`. Can ONLY be called via `execute_tool` wrapper. This is useful for servers with many tools where you want the LLM to discover them as needed rather than receiving all tool definitions upfront.

### Responses Configuration

| Field | Description |
|-------|-------------|
| `storage_path` | Path to BadgerDB storage directory (default: "./responses.db") |
| `ttl_days` | Time-to-live for stored responses in days (default: 30) |

## API Endpoints

### GET /v1/models

Returns aggregated models from all enabled providers.

```bash
# Without authentication
curl http://localhost:12345/v1/models

# With authentication (if token is configured)
curl -H "Authorization: Bearer your-secret-token" http://localhost:12345/v1/models
```

### POST /v1/chat/completions

Creates a chat completion (routed to appropriate provider).

```bash
# Without authentication
curl -X POST http://localhost:12345/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# With authentication (if token is configured)
curl -X POST http://localhost:12345/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### POST /v1/embeddings

Creates embeddings (routed to appropriate provider).

```bash
# Single text input (without authentication)
curl -X POST http://localhost:12345/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-embeddinggemma-300m-qat",
    "input": "Hello world"
  }'

# Multiple text inputs (with authentication)
curl -X POST http://localhost:12345/v1/embeddings \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "text-embedding-embeddinggemma-300m-qat",
    "input": ["Hello", "World"]
  }'
```

### POST /mcp

Model Context Protocol endpoint for tool discovery and execution.

```bash
# List available tools
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

# Search for tools
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0","id":1,"method":"tools/call",
    "params":{"name":"tool_search","arguments":{"query":"calculator"}}
  }'

# Execute a tool
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0","id":1,"method":"tools/call",
    "params":{
      "name":"execute_tool",
      "arguments":{"name":"calculator","arguments":{"operation":"add","a":5,"b":3}}
    }
  }'

# Execute arbitrary code
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0","id":1,"method":"tools/call",
    "params":{
      "name":"execute_code",
      "arguments":{"code":"import mcp\nmcp.return_string(str(2+2))"}
    }
  }'
```

### GET /health

Returns health information including provider status.

```bash
curl http://localhost:12345/health
```

## Responses API Endpoints

The responses API allows storing, retrieving, and managing chat completion responses.

### POST /v1/responses

Create a new response entry.

```bash
curl -X POST http://localhost:12345/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-secret-token" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}],
    "metadata": {"user_id": "123"}
  }'
```

### GET /v1/responses/{id}

Retrieve a specific response by ID.

```bash
curl -H "Authorization: Bearer your-secret-token" \
  http://localhost:12345/v1/responses/resp_abc123
```

### DELETE /v1/responses/{id}

Delete a specific response.

```bash
curl -X DELETE \
  -H "Authorization: Bearer your-secret-token" \
  http://localhost:12345/v1/responses/resp_abc123
```

### GET /v1/responses

List responses with optional filtering.

```bash
# List all responses
curl -H "Authorization: Bearer your-secret-token" \
  http://localhost:12345/v1/responses

# List with limit
curl -H "Authorization: Bearer your-secret-token" \
  "http://localhost:12345/v1/responses?limit=10&order=desc"
```

### POST /v1/responses/{id}/cancel

Cancel an in-progress response.

```bash
curl -X POST \
  -H "Authorization: Bearer your-secret-token" \
  http://localhost:12345/v1/responses/resp_abc123/cancel
```

### POST /v1/responses/compact

Trigger garbage collection and compaction of stored responses.

```bash
curl -X POST \
  -H "Authorization: Bearer your-secret-token" \
  http://localhost:12345/v1/responses/compact
```

## CLI Commands

### Server

```bash
./llmrouter server                    # Run with default config
./llmrouter -config custom.toml server  # Custom config
./llmrouter server -port 8080         # Override port
./llmrouter server -token secret123   # Set bearer token via CLI
```

### Script Execution

```bash
./llmrouter script path/to/script.py arg1 arg2
./llmrouter script -server http://localhost:8080 script.py
./llmrouter script -v script.py       # Verbose output
./llmrouter script -token secret123 script.py  # With authentication
```

### Tool Execution

```bash
./llmrouter tool calculator '{"operation":"add","a":5,"b":3}'
./llmrouter tool -server http://localhost:8080 my_tool args
./llmrouter tool -v tool_name args    # Verbose output
./llmrouter tool -token secret123 calculator args  # With authentication
```

## Building

### Using Taskfile (parallel builds)

```bash
task              # Build for current platform
task build-all    # Build all platforms (parallel)
task release      # Build all with checksums
task clean        # Clean build artifacts
task test         # Run tests
```

### Using Makefile

```bash
make              # Build for current platform
make build-all    # Build all platforms
make release      # Build all with checksums
make clean        # Clean build artifacts
make help         # Show all targets
```

### Supported Platforms

- Linux (AMD64, ARM64)
- macOS (AMD64, ARM64)
- Windows (AMD64, ARM64)

## Documentation

- [Creating Custom Tools](docs/creating_tools.md) - Guide to creating MCP tools
- [MCP Library Reference](docs/mcp_library.md) - `mcp` library functions for tools
- [AI Library Reference](docs/ai_library.md) - `ai` library for LLM integration

## Architecture

### Routing Logic

1. **Model Selection**: Router checks which providers have the requested model
2. **Load Balancing**: Routes to provider with fewest active completions
3. **Failover**: Returns 404 if model not available on any provider

### MCP Server

The MCP server provides three built-in tools:

| Tool | Description |
|------|-------------|
| `tool_search` | Search for available tools by keyword |
| `execute_tool` | Execute a discovered tool with arguments |
| `execute_code` | Execute arbitrary Python/Scriptling code |

### Dynamic Loading

- **Tool scripts**: Loaded from disk on each execution (edit without restart)
- **Tool definitions**: Dynamically scanned from filesystem (add/remove/edit without restart)
- **Libraries**: Loaded on-demand when first imported (edit without restart)

## License

See [LICENSE.txt](LICENSE.txt) for details.
