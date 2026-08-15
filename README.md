<p align="center">
  <img src="icon.svg" width="128" height="128" alt="LLM Router">
</p>

# LLM Router

A unified gateway that aggregates multiple LLM providers behind a single endpoint. Clients use their preferred protocol (OpenAI or Anthropic Messages) and the gateway handles routing, protocol translation, and load balancing.

## Features

- **Multi-Provider**: OpenAI, Claude, Gemini, Ollama, Mistral, ZAi — configure once, route by model name
- **Protocol Translation**: Clients speak OpenAI, Anthropic Messages, or Ollama; the gateway translates as needed in both directions — an Ollama request can be served by an OpenAI/Claude/Gemini/Ollama backend, and vice versa
- **Context-Window Aware**: Each model's context size is auto-discovered from Ollama (`/api/show`) and Gemini, or set per-model / per-provider / globally; surfaced through `/ollama/api/show`, `/ollama/api/tags`, and `/v1/models`
- **Weight-Based Load Balancing**: Distribute load across providers with configurable weights
- **Smart Routing**: Request the `auto` model and a [Scriptling](https://scriptling.dev/) script picks the best provider/model based on tags, load, and request content
- **MCP Aggregator**: Combine tools, resources, and prompts from multiple remote MCP servers with namespace isolation, OAuth support, and per-tool visibility / allow / deny filtering
- **Chat UI**: Built-in interface at `/chat` with personas, conversation history, slash commands, `@prompt` / `@resource` menus, live MCP tool calling, markdown rendering, and `skill://` resources auto-surfaced to the model
- **Personas**: System prompts, default models, and generation parameters — defined in the config file or managed through the admin UI
- **Admin UI**: Optional web interface at `/admin` to manage providers, personas, and MCP servers (create / edit / delete, enable / disable), browse models (with rescan) and tools, and test-run MCP tools directly from the browser
- **Responses API**: OpenAI-compatible responses storage (emulated for all providers)
- **Conversations API**: n8n-compatible conversation management
- **Optional Auth**: Bearer token protection for all endpoints

## Usage Modes

LLM Router ships as a **single binary** that works in two modes:

### Desktop (GUI)

- `llmrouter` with no subcommand opens a native window pointing at the in-process server (glaze/webview — WKWebView on macOS, WebView2 on Windows, WebKitGTK on Linux)
- The HTTP API still binds to the configured port, so external tools (editors, scripts, other MCP clients) work identically
- Desktop mode defaults to localhost binding (`127.0.0.1`) and persistent storage at `~/.llmrouter/data/`
- **Linux runtime dependency:** `sudo apt install libwebkit2gtk-4.1-0` (Ubuntu/Debian) or `sudo dnf install webkit2gtk4.1` (Fedora). The binary auto-detects the available WebKitGTK version at runtime (6.0 preferred, falls back to 4.1). macOS and Windows have no extra dependencies.

### Server (headless)

- `llmrouter server` runs the HTTP API without any GUI
- For Docker, systemd, CI, or any headless environment
- The Docker image is built with `-tags server` to exclude the webview code entirely — the container binary has zero GUI dependencies

### Which to choose?

| Use case | Command |
| -------- | ------- |
| Daily driver on your laptop/desktop | `llmrouter` (opens window) |
| Docker / container deployment | `llmrouter server` |
| systemd / VPS / remote server | `llmrouter server` |
| Headless script / CI | `llmrouter server` |

## Installation

### Homebrew (macOS & Linux)

**App + CLI (macOS — installs `LLM Router.app` to /Applications and `llmrouter` to PATH):**

```bash
brew install --cask paularlott/tap/llmrouter
```

This is the recommended install for macOS desktop users. The cask installs the `.app` for the GUI experience and symlinks the binary into your PATH so `llmrouter server` and other CLI subcommands work from the terminal. Uninstalling removes both.

**CLI only (macOS or Linux — no GUI, just the binary in PATH):**

```bash
brew install paularlott/tap/llmrouter
```

For headless servers, Linux desktops without WebKitGTK, or anyone who only needs the CLI.

### Download from GitHub Releases

Download the latest release for your platform from [github.com/paularlott/llmrouter/releases](https://github.com/paularlott/llmrouter/releases):

| Platform | Architecture          | Download                      |
| -------- | --------------------- | ----------------------------- |
| macOS    | Apple Silicon (ARM64) | `llmrouter-darwin-arm64.zip`  |
| macOS    | Intel (AMD64)         | `llmrouter-darwin-amd64.zip`  |
| Linux    | AMD64                 | `llmrouter-linux-amd64.zip`   |
| Linux    | ARM64                 | `llmrouter-linux-arm64.zip`   |
| Windows  | AMD64                 | `llmrouter-windows-amd64.zip` |
| Windows  | ARM64                 | `llmrouter-windows-arm64.zip` |

macOS downloads contain an `.app` bundle (drag to `/Applications`). Linux/Windows downloads contain a bare binary (extract and place in your PATH).

### Build from Source

Requires [Go 1.26+](https://go.dev/dl/), Node.js (for the web UI build), and [Task](https://taskfile.dev/installation/).

```bash
git clone https://github.com/paularlott/llmrouter.git
cd llmrouter
task            # or: make
./llmrouter     # desktop mode (opens window)
./llmrouter server  # headless server mode
```

## Configuration

LLM Router searches for `config.toml` in the following locations (first match wins):

1. `./config.toml` (current directory)
2. `~/.llmrouter/config.toml`
3. `~/.config/llmrouter/config.toml`
4. `~/.config/config.toml`

In desktop mode (no subcommand), persistent data defaults to `~/.llmrouter/data/`. In server mode, storage is memory-only unless `storage_path` is set.

```toml
[server]
host = "127.0.0.1"            # Default: localhost only. Use 0.0.0.0 for all interfaces
port = 12345
token = "your-secret-token"   # Optional bearer token
admin_password = "admin123"   # Optional: enables admin UI at /admin
storage_path = "./data"       # Omit for memory-only (desktop defaults to ~/.llmrouter/data)
default_context_size = 4096   # Optional: fallback context window (tokens) for models that don't expose one

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
# Context window is auto-discovered from Ollama's /api/show per model, so no
# default_context_size / model_context is needed for ollama providers. Use them
# only to override what Ollama reports.

[[providers]]
name = "openai-fast"
provider = "openai"
token = "sk-..."
enabled = true
default_context_size = 128000   # Fallback for any model without an explicit size

[providers.model_context]       # Explicit per-model overrides (tokens)
"gpt-4o"             = 128000
"gpt-4o-mini"        = 128000
"gpt-3.5-turbo"      = 16385

routes_dir = "./routers"  # Optional: directory of smart-router <model>.toml/.py pairs

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

| Provider  | Default Base URL                              | Embeddings | Model Discovery  | Context Size                              |
| --------- | --------------------------------------------- | ---------- | ---------------- | ----------------------------------------- |
| `openai`  | https://api.openai.com/v1                     | Yes        | Auto             | Configure (`model_context` / `default_context_size`) |
| `claude`  | https://api.anthropic.com/v1                  | No         | **Must specify** | Configure (`model_context` / `default_context_size`) |
| `gemini`  | https://generativelanguage.googleapis.com/... | Yes        | Auto             | Auto (from `inputTokenLimit`)             |
| `ollama`  | https://ollama.com/                          | Yes        | Auto             | Auto (from `/api/show`)                    |
| `mistral` | https://api.mistral.ai/v1                     | Yes        | Auto             | Configure (`model_context` / `default_context_size`) |
| `zai`     | https://api.z.ai/api/paas/v4/                 | Yes        | Auto             | Configure (`model_context` / `default_context_size`) |

`base_url` is optional — each provider has a built-in default. Set it to override (e.g. local LM Studio).

### Context Window

Each model carries a context window (in tokens), resolved per the following precedence (first hit wins):

1. **Per-model override** — `[providers.model_context]` on the serving provider
2. **Discovered from the API** — Ollama `/api/show` (`model_info.context_length`) or Gemini (`inputTokenLimit`)
3. **Per-provider default** — `default_context_size` on the serving provider
4. **Global default** — `server.default_context_size`
5. **4096** floor

When several providers serve the same model, the largest resolved context is kept. Ollama and Gemini providers need no configuration — they introspect the value per model. (For Ollama note that `/api/show` reports the model's trained max context, not the currently loaded `num_ctx` — set `model_context` to override.) OpenAI, Claude, Mistral, and ZAi do not expose context size through their APIs, so set `model_context` (most accurate — it's a property of the model, not the provider) and/or `default_context_size` for them. Both are editable in the admin UI under each provider (hidden only for Gemini, which auto-discovers an authoritative per-model value).

Smart-router virtual models (e.g. `auto`) have no provider to discover from, so they fall back to the global default, then the 4096 floor. Declare an explicit value in the router's `.toml` to override:

```toml
# routers/auto.toml
default_model = "mistralai/ministral-3-3b"
context_size = 128000   # tokens advertised for the "auto" virtual model
```

```toml
[[providers]]
name = "openai"
provider = "openai"
token = "sk-..."
default_context_size = 128000   # used when a model has no explicit entry below

[providers.model_context]       # explicit per-model overrides (tokens)
"gpt-4o"          = 128000
"gpt-4o-mini"     = 128000
"gpt-3.5-turbo"   = 16385
```

The resolved size is surfaced back to clients in three places: `/api/show` and `/ollama/api/show` (`model_info.context_length` + a `num_ctx` parameter), `/api/tags` and `/ollama/api/tags` (a per-model `model_info.context_length`), and `/v1/models` (`context_window` per model). The admin Models page shows a Context column.

For Ollama providers, discovery is done by the native Ollama client in `mcp/ai/ollama` — it queries `/api/tags` and `/api/show` directly. A `base_url` configured for the OpenAI shim (ending in `/v1`) works, since the client strips the `/v1` to reach the native API; the default is `https://ollama.com`.

`model_allowlist` restricts the provider to only the listed models. For Claude this is required (no discovery API). For other providers it is optional.

`model_denylist` excludes specific models from auto-discovery. Ignored when `model_allowlist` is set.

`models` overrides the model list entirely — the provider's `/models` API is still called (for health checks) but its response is discarded and the configured list is used instead. Useful for providers that return no models or an incomplete list.

`model_aliases` maps short or friendly names to real model IDs. Aliases appear in `/v1/models` alongside real models and support the same weight-based load balancing and round-robin behaviour. When multiple providers define the same alias, each provider maps it to its own real model — requests via that alias are distributed across all of them, and each provider receives its own real model name.

### Smart Routing

Smart routing lets a client request a *virtual* model name that triggers a [Scriptling](https://scriptling.dev/) script, which picks the real provider and model. You define routers as files in a folder: each `<model>.toml` (plus an optional `<model>.py` companion) becomes a router triggered by the model name `<model>` (the filename stem).

```toml
routes_dir = "./routers"   # directory of <model>.toml/.py pairs
```

For example, `./routers/auto.toml` + `./routers/auto.py` make clients that request the model `auto` run `auto.py`:

```toml
# routers/auto.toml — the stem ("auto") is the model name clients send
enabled = true
default_model = "mistralai/ministral-3-3b"   # used when the script returns nothing or fails
context_size = 128000                          # optional: tokens advertised for this virtual model

[vars]              # optional key-value pairs exposed to the script as the `vars` library
openai_key = "sk-..."
```

```python
# routers/auto.py
import router
router.set_model("mistralai/mistral-small-latest")
```

A `.toml` without a `.py` companion is a pure alias — every request for that name goes to `default_model`. A `.py` without a `.toml` is just an importable library (shared by all routers in the folder). Router names must be a single path segment (no `/`); a name that collides with a real provider model is skipped. Every router model is injected into `/v1/models` so clients can discover it.

Shared libraries placed in the routers folder are importable by every router. Add more search directories with the global, repeatable `--libpath` flag (`libpath` / `lib_paths` in config), shared with the scriptling MCP tools. The folder and every libpath dir are watched — script, config, or library changes are picked up within ~100 ms with no restart.

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

| Library                    | Description                                   |
| -------------------------- | --------------------------------------------- |
| `requests`                 | HTTP client                                   |
| `secrets`                  | Cryptographically strong random numbers       |
| `html.parser`              | HTML/XHTML parser                             |
| `logging`                  | Logging to the router log                     |
| `yaml`                     | YAML parsing                                  |
| `toml`                     | TOML parsing                                  |
| `sys`                      | System parameters                             |
| `scriptling.ai`            | AI/LLM client for OpenAI-compatible APIs      |
| `scriptling.ai.agent`      | Agentic AI loop with automatic tool execution |
| `scriptling.mcp`           | MCP tool interaction                          |
| `scriptling.toon`          | TOON encoding/decoding                        |
| `scriptling.similarity`    | String matching and similarity utilities      |
| `scriptling.net.resolve`   | DNS resolution (IP, SRV, srv+http URLs)       |
| `scriptling.template.html` | HTML template rendering                       |
| `scriptling.template.text` | Text template rendering                       |
| `scriptling.runtime`       | Background tasks, KV store, sync primitives   |

Filesystem access (`os`, `pathlib`, `glob`), subprocess execution, and `wait_for` are not available in routing scripts.

#### Script Variables

Use the `[vars]` table in a router's `.toml` to pass tokens or other config to the script without hard-coding them:

```toml
# routers/auto.toml
[vars]
openai_key = "sk-..."
```

```python
import vars
import scriptling.ai as ai

client = ai.Client("", api_key=vars.openai_key)   # attribute access
key = vars.get("openai_key")                       # or dynamic lookup
key = vars.get("missing", "")                      # with a default
```

All values are strings. `vars` is always available (even with no vars defined), so `vars.get(name, default="")` is always callable.

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

### Scripting Tools

Define custom MCP tools using `.toml` (metadata) and `.py` (implementation) file pairs. Tools are loaded from a configurable directory and automatically reloaded when files change.

```bash
./llmrouter server --tools-dir ./tools --plugin-dir ./plugins --libpath ./libs
```

**Directory structure:**

```
tools/
├── calculator.toml
├── calculator.py
├── weather.toml
└── weather.py
```

**Tool metadata (`.toml`):**

```toml
description = "Calculate the sum of two numbers"
keywords = ["math", "add", "sum"]

[[parameters]]
name = "a"
type = "int"
description = "First number"
required = true

[[parameters]]
name = "b"
type = "int"
description = "Second number"
required = true
```

**Tool implementation (`.py`):**

```python
import scriptling.mcp.tool as tool

a = tool.get_int("a")
b = tool.get_int("b")

result = a + b
tool.return_string(f"{a} + {b} = {result}")
```

**Configuration:**

```toml
[scripting]
tools_dir = "./tools"              # Directory containing .toml/.py tool pairs
plugin_dirs = ["./plugins"]        # Directories containing plugin executables
lib_paths = ["./libs"]             # Additional directories for scriptling libraries
```

Tools can be marked as `discoverable = true` to hide them from `tools/list` and make them searchable via `tool_search` only.

### Chat UI

When `admin_password` is set and a personas directory is configured, a built-in chat interface is available at `/chat` with conversation history, personas, slash commands, `@prompt` / `@resource` menus, MCP tool calling, and markdown rendering.

```bash
./llmrouter server --personas-dir ./personas --commands-dir ./commands --resources-dir ./resources
```

**Skills:** Resources with a `skill://` URI prefix are automatically surfaced to the LLM. On every chat request, the router queries the MCP server for `skill://` resources and appends their names and descriptions to the persona's system prompt. A virtual tool (`lmchatkit__get_skill`) is injected into the tool list — the model calls it to retrieve a skill's full instructions on demand. The tool routes to the standard MCP `ReadResource` API, so skills work from any source (files, remote servers, or custom providers). The tool is auto-approved (no user prompt) since it's a read-only context fetch. Skills are transient — the stored conversation is not modified; the augmentation is recomputed on each request.

Example skill resource directory structure:
```
resources/
└── skill/
    ├── golang.md    → skill://golang.md
    └── testing.md   → skill://testing.md
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

All endpoints are available under both `/v1/` and `/ollama/` base paths. For example, models can be listed at either `/v1/models` or `/ollama/v1/models`.

### Chat & Models

```bash
GET  /v1/models
POST /v1/chat/completions          # OpenAI format, streaming supported
POST /v1/messages                  # Anthropic Messages format
POST /v1/messages/count_tokens     # Anthropic token counting (emulated)
POST /v1/embeddings
GET  /health
```

OpenAI chat completion requests preserve provider-specific top-level fields when forwarding upstream, including fields produced by client-side `extra_body` options such as ZAi thinking-mode settings.

### Ollama Compatible

The native Ollama API is served at `/api/*` directly on the gateway's ports, so Ollama clients (VS Code Copilot, LM Studio, etc.) can point at `http://host:port` and work out of the box. The same endpoints are also mirrored under `/ollama/api/*` (and the OpenAI-compatible surface under `/ollama/v1/*`).

```bash
GET  /api/version           # API version info
GET  /api/tags              # List models (each carries model_info.context_length)
GET  /api/ps                # List running models
POST /api/chat              # Chat with messages (supports images)
POST /api/generate          # Generate from prompt (supports images)
POST /api/embed             # Embeddings (batch)
POST /api/embeddings        # Embeddings (single)
POST /api/show              # Model details (model_info.context_length + num_ctx)
```

`/api/*` does not collide with the OpenAI API (`/v1/*`) or the admin API (`/admin/api/*`). Every endpoint above is also available with an `/ollama` prefix (e.g. `/ollama/api/chat`).

`/api/show` and `/api/tags` return each model's resolved context window (see [Context Window](#context-window)), which is the main reason to route Ollama clients through the gateway — a single source of truth for context size across mixed providers. Inbound Ollama requests are translated to whatever the serving provider speaks: an `/api/chat` request for a model backed by OpenAI, Claude, Gemini, or another Ollama is forwarded upstream in that provider's native wire format and the reply is translated back to Ollama format.

Images sent via Ollama's `images` field are automatically converted with the correct media type (JPEG, PNG, GIF, WebP) based on file signature detection.

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

When `server.admin_password` is set, a web-based admin interface is available at `/admin`. From it you can manage providers, personas, and MCP servers (create / edit / delete, enable / disable), browse models and trigger a rescan, inspect tools / resources / prompts, and test-run MCP tools directly from the browser. Items created here are stored under `storage_path` and take effect live (config-file entries are shown read-only alongside them).

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
./llmrouter                                   # Desktop build: open GUI window
                                              # Server build:  equivalent to `server`
./llmrouter server                            # Start headless server
./llmrouter -config custom.toml server        # Custom config
./llmrouter server -port 8080                 # Override port
./llmrouter server -token secret123           # Set bearer token
./llmrouter server --default-context-size 8192  # Fallback context window (tokens)
./llmrouter server --tools-dir ./tools        # Load scriptling MCP tools
./llmrouter server --plugin-dir ./plugins     # Load scriptling plugins
./llmrouter server --libpath ./libs           # Add library directories
./llmrouter models                            # List available models
./llmrouter ask gpt-4o "What is 2+2?"         # Ask a model a question
./llmrouter tool calculator '{"op":"add","a":1,"b":2}'  # Execute MCP tool
```

All `server` flags are also accepted by the root command in desktop mode, so `./llmrouter -p 8080` tunes the API port for the desktop session without typing `server`.

## Development

### Building

[Task](https://taskfile.dev/) is the canonical build system.

```bash
task              # Build for current platform
task build-all    # Build all platforms + ZIP archives
task release      # Build, tag, publish GitHub release + brew formulas
```

The Docker image is built with `-tags server` internally (excludes the webview/glaze code), so the published container is a minimal headless-only binary.

Supported platforms: macOS, Linux, Windows × AMD64/ARM64.

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
   ├── Scripting Tools (local .toml/.py tool pairs with file watching)
   ├── Responses API   (emulated for all providers, in-memory index only)
   └── Conversations   (n8n-compatible, stored in SnapshotKV)
```

Protocol translation is handled by the `mcp/ai` package — the gateway always works in OpenAI format internally and translates at the edges.

## License

See [LICENSE.txt](LICENSE.txt) for details.
