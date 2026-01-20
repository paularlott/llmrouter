# MCP Server Implementation

## Overview

The llmrouter MCP server provides fully dynamic tool loading with per-tool visibility control:

1. **Built-in tools** (`execute_code`) - registered globally as native
2. **Script tools** - loaded dynamically per-request via provider, with per-tool visibility
3. **Remote MCP servers** - with configurable visibility (native or ondemand)
4. **Single endpoint with mode selection** - `/mcp` with header or query param for mode

## Architecture

### Dynamic Script Tools

**All script tools are loaded dynamically per-request:**

- Provider scans filesystem on each request
- Tools can be added/removed/modified without restart
- Changes are picked up immediately on next request
- Per-tool visibility can be changed between requests
- No global registration required

### Tool Mode Selection

The `/mcp` endpoint supports two modes, controlled via the `X-MCP-Tool-Mode` header or `tool_mode` query parameter:

**Normal mode** (default):

- Native-visibility tools appear in `tools/list`
- OnDemand-visibility tools are hidden but searchable via `tool_search`
- If any ondemand tools exist, `tool_search` and `execute_tool` are available
- All tools are directly callable by name

**Discovery mode** (`X-MCP-Tool-Mode: discovery` or `?tool_mode=discovery`):

- Only `tool_search` and `execute_tool` appear in `tools/list`
- ALL tools (native and ondemand) are searchable via `tool_search`
- Tools must be called via `execute_tool` (or directly if name is known)
- Ideal for large tool sets to minimize context window usage

## Configuration

### Script Tools

Create a `tool.toml` in each tool directory:

```toml
name = "my_tool"
description = "Tool description"
keywords = ["keyword1", "keyword2"]  # For tool_search
script = "script.py"
visibility = "native"  # or "ondemand" (defaults to "native")

[parameters.param_name]
type = "string"  # or "number", "boolean"
description = "Parameter description"
required = true
```

**Per-tool visibility:**

- `visibility = "native"` (default): Tool appears in `tools/list` on `/mcp` endpoint
- `visibility = "ondemand"`: Tool is hidden from `tools/list` but searchable via `tool_search`

### Remote MCP Servers

```toml
[[mcp.remote_servers]]
namespace = "remote"
url = "https://example.com/mcp"
token = "bearer-token"
tool_visibility = "native"  # or "ondemand"
```

**Tool visibility:**

- `tool_visibility = "native"`: Tools visible in `tools/list`
- `tool_visibility = "ondemand"`: Tools only via `tool_search`

## Implementation Details

### ScriptToolProvider

Implements `mcp.ToolProvider` interface for dynamic script tools:

```go
type ScriptToolProvider struct {
    mcpServer  *MCPServer
    visibility string  // "native" or "ondemand"
}

// Two provider factories:
func NewNativeScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider
func NewOnDemandScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider
```

**Behavior:**

- Each provider filters tools based on their `visibility` setting
- Native provider returns tools with `visibility="native"` or no visibility (default)
- OnDemand provider returns tools with `visibility="ondemand"`
- Keywords on all tools are used for `tool_search`

### Request Handling

```go
// /mcp endpoint - native mode
func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
    nativeProvider := NewNativeScriptToolProvider(m)
    onDemandProvider := NewOnDemandScriptToolProvider(m)

    // Native provider tools appear in tools/list (unless discovery mode header is set)
    ctx := mcp.WithToolProviders(r.Context(), nativeProvider)

    // OnDemand provider tools are searchable but hidden
    onDemandTools, _ := onDemandProvider.GetTools(r.Context())
    if len(onDemandTools) > 0 {
        ctx = mcp.WithOnDemandToolProviders(ctx, onDemandProvider)
    }

    // MCP server handles mode from X-MCP-Tool-Mode header or tool_mode query param
    m.server.HandleRequest(w, r.WithContext(ctx))
}
```

### Tool Registration Flow

**Server Startup:**

1. Register `execute_code` as native tool (no keywords)
2. Connect to remote MCP servers with appropriate visibility

**Per Request:**

1. Create two provider instances (native and ondemand)
2. Each provider scans filesystem and filters by visibility
3. Native tools added via `WithToolProviders`
4. OnDemand tools added via `WithOnDemandToolProviders` (if any exist)
5. MCP library handles tool visibility and discovery

## Dynamic Tool Loading

All script tool changes are picked up immediately:

1. **Add tool**: Create new directory with `tool.toml` and script → visible on next request
2. **Modify tool**: Edit `tool.toml` or script file → changes on next request
3. **Remove tool**: Delete tool directory → gone on next request
4. **Change visibility**: Edit `visibility` in `tool.toml` → changes on next request
5. **Change description/keywords**: Edit `tool.toml` → updated on next request

**No server restart needed for any script tool changes.**

## Tool Discovery

### Normal mode (default):

- Native tools appear in `tools/list`
- OnDemand tools are hidden from `tools/list`
- If any ondemand tools exist, `tool_search` and `execute_tool` are available
- Both native and ondemand tools are searchable via `tool_search`
- All tools are directly callable by name

### Discovery mode (`X-MCP-Tool-Mode: discovery` or `?tool_mode=discovery`):

- Only `tool_search` and `execute_tool` appear in `tools/list`
- ALL tools (native, ondemand, builtin, remote) are searchable via `tool_search`
- Tools should be called via `execute_tool` for consistency

## Testing

Run tests:

```bash
go test ./...
```

Tests cover:

- Basic tool loading
- Dynamic tool addition/removal/modification
- Parameter type handling
- Native vs ondemand tool visibility
- Discovery mode
- tool_search functionality
- Integration with MCP server

## Examples

### Native Tool (visible in tools/list)

```toml
# example-tools/calculator/tool.toml
name = "calculator"
description = "Perform calculations"
keywords = ["math", "calculate", "arithmetic"]
script = "calculator.py"
visibility = "native"  # or omit for default

[parameters.operation]
type = "string"
description = "Operation: add, subtract, multiply, divide"
required = true

[parameters.a]
type = "number"
description = "First number"
required = true

[parameters.b]
type = "number"
description = "Second number"
required = true
```

### OnDemand Tool (searchable only)

```toml
# example-tools/email_sender/tool.toml
name = "send_email"
description = "Send an email to a recipient"
keywords = ["email", "send", "message", "notification", "smtp"]
script = "send_email.py"
visibility = "ondemand"

[parameters.to]
type = "string"
description = "Recipient email address"
required = true

[parameters.subject]
type = "string"
description = "Email subject"
required = true

[parameters.body]
type = "string"
description = "Email body content"
required = true
```

**Behavior on `/mcp` endpoint:**

- Not visible in `tools/list`
- `tool_search` and `execute_tool` ARE visible (because ondemand tools exist)
- Searchable via `tool_search(query="email")`
- Callable via `execute_tool(name="send_email", arguments={...})`
- Also directly callable if name is known

## Troubleshooting

**Tool not appearing in tools/list:**

- Check `visibility` setting in `tool.toml` (must be "native" or omitted)
- Verify not using discovery mode (no `X-MCP-Tool-Mode: discovery` header)
- Ensure `tool.toml` has required fields (name, description, script)

**Tool not found via tool_search:**

- Verify `keywords` are set in `tool.toml`
- Check tool directory is in configured `tools_path`
- Ensure `tool.toml` parses correctly (no syntax errors)

**tool_search not available:**

- Only available when there are ondemand tools OR using discovery mode
- Register at least one tool with `visibility = "ondemand"`
- Or use `X-MCP-Tool-Mode: discovery` header or `?tool_mode=discovery` query param

**Changes not picked up:**

- Script tools: Changes are immediate (no restart needed)
- Built-in tools: Requires server restart
- Remote servers: Requires server restart
- Check file permissions and syntax errors in `tool.toml`

## Summary

The implementation provides:

- ✅ Fully dynamic script tools (no restart needed)
- ✅ Per-tool visibility control (native vs ondemand)
- ✅ Single endpoint with mode selection via header or query param
- ✅ Per-request tool loading with visibility filtering
- ✅ Support for remote MCP servers with visibility control
- ✅ Automatic tool discovery with keywords
- ✅ `tool_search` and `execute_tool` only appear when needed
- ✅ Thread-safe concurrent request handling
