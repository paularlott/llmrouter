# LLM Router

A unified gateway that aggregates multiple LLM providers behind a single endpoint. Clients use their preferred protocol (OpenAI or Anthropic Messages) and the gateway handles routing, protocol translation, and load balancing.

## Features

- **Multi-Provider**: OpenAI, Claude, Gemini, Ollama, Mistral, ZAi — configure once, route by model name
- **Protocol Translation**: Clients speak OpenAI or Messages (Claude) format; the gateway translates as needed
- **Weight-Based Load Balancing**: Distribute load across providers with configurable weights
- **Smart Routing**: Request the `auto` model and a [Scriptling](https://scriptling.dev/) script picks the best provider/model based on tags, load, and request content
- **MCP Aggregator**: Aggregate tools from multiple remote MCP servers with namespace isolation
- **Responses API**: OpenAI-compatible responses storage (emulated for all providers)
- **Conversations API**: n8n-compatible conversation management
- **Optional Auth**: Bearer token protection for all endpoints
- **Admin UI**: Optional web interface for monitoring providers, models, and MCP servers

## Installation

### Homebrew (macOS & Linux)

```bash
brew tap paularlott/tap
brew install llmrouter
```

### Download from GitHub Releases

Download the latest release for your platform from [github.com/paularlott/llmrouter/releases](https://github.com/paularlott/llmrouter/releases):

| Platform | Architecture | Download |
|----------|-------------|----------|
| macOS | Intel (AMD64) | `llmrouter-darwin-amd64.zip` |
| macOS | Apple Silicon (ARM64) | `llmrouter-darwin-arm64.zip` |
| Linux | AMD64 | `llmrouter-linux-amd64.zip` |
| Linux | ARM64 | `llmrouter-linux-arm64.zip` |
| Windows | AMD64 | `llmrouter-windows-amd64.zip` |
| Windows | ARM64 | `llmrouter-windows-arm64.zip` |

Extract the archive and place the binary in your PATH.

### Build from Source

```bash
git clone https://github.com/paularlott/llmrouter.git
cd llmrouter
go build -o llmrouter .
./llmrouter server
./llmrouter -config /path/to/config.toml server
```

## Configuration

```toml
[server]
host = "0.0.0.0"
port = 12345
token = "your-secret-token"   # Optional bearer token
admin_password = "admin123"   # Optional: enables admin UI at /admin
storage_path = "./data"  # Omit for memory-only storage (under [server])

[logging]
level = "info"    # trace | debug | info | warn | error
format = "console" # console | json

[responses]
ttl_days = 30

[conversations]
ttl_days = 30

[[providers]]
name = "openai"
provider = "openai"           # openai | claude | gemini | ollama | mistral | zai
token = "sk-..."
enabled = true
weight = 1.0                  # 0.0-2.0, default 1.0; higher = preferred
tags = ["capable", "expensive"]  # optional tags for smart routing

[providers.model_tags]        # optional per-model tags
"gpt-4o"      = ["capable", "expensive"]
"gpt-4o-mini" = ["fast", "cheap"]

[providers.model_aliases]     # optional alias -> real model name, resolved per-provider
"gpt4"     = "gpt-4o"
"gpt4-mini" = "gpt-4o-mini"

[[providers]]
name = "anthropic"
provider = "claude"
token = "sk-ant-..."
enabled = true
model_allowlist = ["claude-opus-4-5", "claude-sonnet-4-5"]  # Required for Claude
tags = ["capable"]

[providers.model_aliases]     # same alias "fast", different real model on this provider
"fast" = "claude-sonnet-4-5"

[providers.model_tags]
"claude-opus-4-5"   = ["capable", "expensive"]
"claude-sonnet-4-5" = ["capable", "fast"]

[[providers]]
name = "google"
provider = "gemini"
token = "your-google-key"
enabled = true
model_allowlist = ["gemini-2.5-flash-lite"]  # Optional: restrict to specific models
models = ["gemini-2.5-flash-lite"]           # Optional: override model list entirely (still health-checks the API)

[[providers]]
name = "local"
provider = "ollama"
base_url = "http://localhost:11434/v1"
enabled = true

[smart_routing]
enabled = false
script = "router.py"  # Scriptling script for routing decisions
default_model = "mistralai/ministral-3-3b"  # Fallback if script returns nothing
libdir = "./router_libs"  # Optional: directory of .py script libraries

[mcp]
[[mcp.remote_servers]]
namespace = "tools"
url = "https://tools.example.com/mcp"
token = "secret"
tool_visibility = "native"    # native | discoverable
tool_allowlist = ["search", "query"]  # Optional: only these tools are enabled
tool_denylist = ["delete"]           # Optional: these tools are disabled
```

### Provider Types

| Provider  | Default Base URL                              | Embeddings | Model Discovery  |
| --------- | --------------------------------------------- | ---------- | ---------------- |
| `openai`  | https://api.openai.com/v1                     | Yes        | Auto             |
| `claude`  | https://api.anthropic.com/v1                  | No         | **Must specify** |
| `gemini`  | https://generativelanguage.googleapis.com/... | Yes        | Auto             |
| `ollama`  | https://ollama.com/v1/                        | Yes        | Auto             |
| `mistral` | https://api.mistral.ai/v1                     | Yes        | Auto             |
| `zai`     | https://api.z.ai/api/paas/v4/                 | Yes        | Auto             |

`base_url` is optional — each provider has a built-in default. Set it to override (e.g. local LM Studio).

`model_allowlist` restricts the provider to only the listed models. For Claude this is required (no discovery API). For other providers it is optional.

`model_denylist` excludes specific models from auto-discovery. Ignored when `model_allowlist` is set.

`models` overrides the model list entirely — the provider's `/models` API is still called (for health checks) but its response is discarded and the configured list is used instead. Useful for providers that return no models or an incomplete list.

`model_aliases` maps short or friendly names to real model IDs. Aliases appear in `/v1/models` alongside real models and support the same weight-based load balancing and round-robin behaviour. When multiple providers define the same alias, each provider maps it to its own real model — requests via that alias are distributed across all of them, and each provider receives its own real model name.

### Smart Routing

When a client requests the model name `auto`, the router runs a [Scriptling](https://scriptling.dev/) script to pick the best provider and model. If the script returns nothing or fails, `default_model` is used.

`libdir` is an optional directory of `.py` files. Each file is registered as a script library (named after the file without the `.py` extension) and is available for `import` in the routing script. The directory is watched for changes — any modification triggers a full VM pool rebuild.

```toml
[smart_routing]
enabled = true
script = "router.py"  # Scriptling script for routing decisions
default_model = "mistralai/ministral-3-3b"

[smart_routing.vars]  # Optional key-value pairs exposed to the script
openai_key = "sk-..."
my_endpoint = "https://api.example.com"
```

The `auto` model appears in `/v1/models` so clients can discover it.

#### Provider and Model Tags

Tags are arbitrary strings assigned to providers and individual models. The routing script uses them to select the right provider/model for each request.

```toml
[[providers]]
name = "mistral"
provider = "mistral"
token = "..."
enabled = true
tags = ["fast", "cheap"]           # provider-level tags

[providers.model_tags]
"mistralai/ministral-3-3b"      = ["small", "fast", "cheap"]
"mistralai/mistral-small-latest" = ["small", "fast"]
```

The routing script can then query by tag:

```python
import router

req = router.get_request()

# Route tool-heavy requests to a capable model
if router.is_chat_completion() and len(req["tools"]) > 0:
    models = router.models_by_tag("capable")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

Use `router.model_tags(model_id)` to narrow a candidate list by a secondary tag:

```python
import router

# Start broad: all "capable" models
candidates = router.models_by_tag("capable")

# Narrow: prefer "super_fast" within that set
fast = [m for m in candidates if "super_fast" in router.model_tags(m)]
cheap = [m for m in candidates if "cheap" in router.model_tags(m)]

models = fast or cheap or candidates
if models:
    router.set_model(models[0])
```

See [docs/scriptling-router-library.md](docs/scriptling-router-library.md) for the full script API reference.

#### Script Libraries

Every routing script has access to all Scriptling standard libraries (`json`, `re`, `math`, `random`, `hashlib`, `base64`, `uuid`, `datetime`, `time`, `urllib`, etc.) plus the following extended and Scriptling-specific libraries:

| Library | Description |
|---------|-------------|
| `requests` | HTTP client |
| `secrets` | Cryptographically strong random numbers |
| `html.parser` | HTML/XHTML parser |
| `logging` | Logging to the router log |
| `yaml` | YAML parsing |
| `toml` | TOML parsing |
| `sys` | System parameters |
| `scriptling.ai` | AI/LLM client for OpenAI-compatible APIs |
| `scriptling.ai.agent` | Agentic AI loop with automatic tool execution |
| `scriptling.mcp` | MCP tool interaction |
| `scriptling.toon` | TOON encoding/decoding |
| `scriptling.similarity` | String matching and similarity utilities |
| `scriptling.template.html` | HTML template rendering |
| `scriptling.template.text` | Text template rendering |
| `scriptling.runtime` | Background tasks, KV store, sync primitives |

Filesystem access (`os`, `pathlib`, `glob`), subprocess execution, and `wait_for` are not available in routing scripts.

#### Script Variables

Use `[smart_routing.vars]` to pass tokens or other config to the script without hard-coding them:

```toml
[smart_routing.vars]
openai_key = "sk-..."
```

```python
import vars
import scriptling.ai as ai

client = ai.Client("", api_key=vars.openai_key)
```

### Weight-Based Load Balancing

When multiple providers serve the same model, the router selects using `score = active_completions / weight`. Lower score wins.

| Weight | Effect                                |
| ------ | ------------------------------------- |
| `0.0`  | Last resort only                      |
| `1.0`  | Normal (default)                      |
| `2.0`  | Preferred — gets 2× the traffic share |

### MCP Tool Visibility

| Mode           | Behavior                                            |
| -------------- | --------------------------------------------------- |
| `native`       | Tools appear in `tools/list`, directly callable     |
| `discoverable` | Hidden from list, searchable via `tool_search` only |

### MCP Tool Filtering

Tools from remote MCP servers can be filtered using `tool_allowlist` or `tool_denylist`:

- **`tool_allowlist`**: When defined, only the listed tools are enabled. All other tools are disabled.
- **`tool_denylist`**: When defined, all tools are enabled except those in the list.

Note: If both are defined, `tool_allowlist` takes precedence and `tool_denylist` is ignored.

```toml
[[mcp.remote_servers]]
namespace = "github"
url = "https://github.example.com/mcp"
token = "secret"
tool_visibility = "native"
tool_allowlist = ["search_repos", "get_issue", "create_issue"]  # Only these 3 tools are enabled

[[mcp.remote_servers]]
namespace = "slack"
url = "https://slack.example.com/mcp"
token = "secret"
tool_visibility = "native"
tool_denylist = ["delete_message", "ban_user"]  # All tools enabled except these 2
```

### MCP OAuth Authentication

For MCP servers that require OAuth authentication, configure the OAuth fields instead of `token`:

```toml
[[mcp.remote_servers]]
namespace = "my-oauth-service"
url = "https://api.example.com/mcp"
auth_type = "oauth"
oauth_client_id = "your-client-id"
oauth_token_url = "https://auth.example.com/token"
oauth_access_token = "current-access-token"
oauth_refresh_token = "refresh-token"  # Optional: for token refresh
```

## API Endpoints

### Chat & Models

```bash
GET  /v1/models
POST /v1/chat/completions          # OpenAI format, streaming supported
POST /v1/messages                  # Anthropic Messages format
POST /v1/messages/count_tokens     # Anthropic token counting (emulated)
POST /v1/embeddings
GET  /health
```

### Responses API

```bash
POST   /v1/responses
GET    /v1/responses/{id}
DELETE /v1/responses/{id}
GET    /v1/responses
POST   /v1/responses/{id}/cancel
POST   /v1/responses/compact
```

### Conversations API

```bash
POST   /v1/conversations
GET    /v1/conversations/{id}
POST   /v1/conversations/{id}
DELETE /v1/conversations/{id}
GET    /v1/conversations/{conversation_id}/items
POST   /v1/conversations/{conversation_id}/items
GET    /v1/conversations/{conversation_id}/items/{item_id}
DELETE /v1/conversations/{conversation_id}/items/{item_id}
```

### MCP

```bash
POST /mcp    # MCP protocol — aggregates tools from all configured remote servers
```

### Admin UI

When `server.admin_password` is set, a web-based admin interface is available at `/admin` for monitoring providers, models, and MCP servers.

```bash
GET /admin         # Admin UI (requires password login)
GET /admin/login   # Login page
```

### Authentication

When `server.token` is set, all endpoints except `/health` require:

```
Authorization: Bearer your-secret-token
```

## CLI

```bash
./llmrouter server                          # Start server
./llmrouter -config custom.toml server      # Custom config
./llmrouter server -port 8080               # Override port
./llmrouter server -token secret123         # Set bearer token
./llmrouter models                          # List available models
./llmrouter ask gpt-4o "What is 2+2?"       # Ask a model a question
./llmrouter tool calculator '{"op":"add","a":1,"b":2}'  # Execute MCP tool
```

## Development

### Building

```bash
task              # Build for current platform
task build-all    # Build all platforms with ZIP archives
task release      # Build, create GitHub release, and update Homebrew formula
make              # Alternative via Makefile
```

Supported platforms: Linux, macOS, Windows × AMD64/ARM64.

## Architecture

```
Client (OpenAI or Messages protocol)
        │
        ▼
   LLM Router
   ├── Protocol Layer  (OpenAI ↔ Messages translation via mcp/ai)
   ├── Routing Layer   (model → provider, weight-based load balancing)
   ├── Smart Routing   (Scriptling script, tag-based selection, hot-reload)
   ├── Provider Layer  (openai | claude | gemini | ollama | mistral | zai)
   ├── MCP Aggregator  (remote MCP servers with namespace + visibility control)
   ├── Responses API   (emulated for all providers, in-memory index only)
   └── Conversations   (n8n-compatible, stored in SnapshotKV)
```

Protocol translation is handled by the `mcp/ai` package — the gateway always works in OpenAI format internally and translates at the edges.

## License

See [LICENSE.txt](LICENSE.txt) for details.
