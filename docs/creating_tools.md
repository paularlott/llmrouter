# Creating Custom Tools

This guide explains how to create custom tools for the LLM Router MCP server using Scriptling.

## Tool Structure

Tools are created in the `tools_path` directory (configured in `config.toml`) with the following folder structure:

```
example-tools/
├── my_tool/
│   ├── tool.toml
│   └── script.py
├── calculator/
│   ├── tool.toml
│   └── calculator.py
└── weather_tool/
    ├── tool.toml
    └── weather.py
```

Each tool requires:

1. A **directory** named after your tool
2. A **tool.toml** configuration file defining the tool's metadata and parameters
3. A **script file** (Python/Scriptling) that implements the tool logic

## tool.toml Configuration

The `tool.toml` file defines your tool's metadata, parameters, and keywords for discovery:

```toml
name = "my_custom_tool"
description = "A clear description of what this tool does"
keywords = ["keyword1", "keyword2", "keyword3"]
script = "script.py"
visibility = "ondemand"  # Optional: "native" (default) or "ondemand"

[parameters.name]
type = "string"
description = "The name of the person to greet"
required = true

[parameters.count]
type = "number"
description = "Number of times to repeat the greeting"
required = false

[parameters.uppercase]
type = "boolean"
description = "Whether to output in uppercase"
required = false
```

### Configuration Fields

| Field         | Description                                                                                                       | Required | Default    |
| ------------- | ----------------------------------------------------------------------------------------------------------------- | -------- | ---------- |
| `name`        | Tool identifier (used in API calls). Defaults to directory name if not specified.                                 | No       | -          |
| `description` | Human-readable description shown in tool discovery.                                                               | Yes      | -          |
| `keywords`    | Array of keywords for tool search/discovery.                                                                      | No       | -          |
| `script`      | Filename of the script to execute (relative to tool directory).                                                   | Yes      | -          |
| `visibility`  | Tool visibility mode: `"native"` (appears in tools/list) or `"ondemand"` (hidden but searchable via tool_search). | No       | `"native"` |
| `parameters`  | Map of parameter definitions.                                                                                     | No       | -          |

### Parameter Types

| Type      | Description                         | Python Type |
| --------- | ----------------------------------- | ----------- |
| `string`  | Text values                         | `str`       |
| `number`  | Numeric values (integers or floats) | `float`     |
| `boolean` | True/false values                   | `bool`      |

### Parameter Properties

| Property      | Description                              | Default  |
| ------------- | ---------------------------------------- | -------- |
| `type`        | Parameter type (string, number, boolean) | Required |
| `description` | Human-readable description               | Required |
| `required`    | Whether the parameter must be provided   | `false`  |

### Tool Visibility

The `visibility` field controls how your tool is exposed to MCP clients:

| Visibility           | Description                                                                                                                                              |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `"native"` (default) | Tool appears in `tools/list` and can be called directly. Suitable for commonly used tools.                                                               |
| `"ondemand"`         | Tool is hidden from `tools/list` but can be discovered via `tool_search` and executed via `execute_tool`. Suitable for specialized or rarely used tools. |

**When to use each visibility:**

- **Native (`"native"`)**: Use for tools that are:
  - Frequently used
  - Part of core functionality
  - Expected to be visible in tool listings

- **OnDemand (`"ondemand"`)**: Use for tools that are:
  - Specialized or experimental
  - Used infrequently
  - Part of a large toolset where showing all tools would be overwhelming

**Note:** The unified `/mcp` endpoint supports two modes. Use the `X-MCP-Tool-Mode: discovery` header or `?tool_mode=discovery` query param to enable discovery mode. In discovery mode, ALL tools (regardless of their individual visibility setting) will be hidden from `tools/list` but remain searchable.

## Tool Script

Tool scripts are written in Python/Scriptling and use the `llmr.mcp` and `llmr.ai` libraries.

### Basic Tool Example

```python
import llmr.mcp

def main():
    # Get parameters
    name = llmr.mcp.get("name")
    count = llmr.mcp.get("count", 1)  # Default value of 1
    uppercase = llmr.mcp.get("uppercase", False)

    # Validate required parameter
    if not name:
        llmr.mcp.return_string("Error: name parameter is required")
        return

    # Build result
    greeting = f"Hello, {name}!"
    if uppercase:
        greeting = greeting.upper()

    result = "\n".join([greeting] * int(count))

    # Return result
    llmr.mcp.return_string(result)

main()
```

### Tool with AI Integration

Tools can use the AI library to interact with LLMs:

```python
import llmr.mcp
import llmr.ai

def main():
    question = llmr.mcp.get("question")
    model = llmr.mcp.get("model", "mistralai/devstral-small-2-2512")

    if not question:
        llmr.mcp.return_string("Error: question parameter is required")
        return

    # Create messages for the AI
    messages = [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": question}
    ]

    # Get AI completion (automatic tool calling is handled internally)
    response = llmr.ai.completion(model, messages)

    llmr.mcp.return_string(response)

main()
```

### Using Custom Libraries

Tools can import custom libraries from the `libraries_path` directory:

```python
import llmr.mcp
import string_utils  # Loaded from libraries_path/string_utils.py

def main():
    text = llmr.mcp.get("text")
    result = string_utils.to_uppercase(text)
    llmr.mcp.return_string(result)

main()
```

## Dynamic Loading

The LLM Router supports dynamic tool loading through the MCP library's ToolProvider pattern:

### How It Works

The LLM Router uses the ToolProvider pattern for dynamic tool discovery:

1. **Native Tools** (`visibility = "native"` or not set)
   - Dynamically loaded from disk on **each request**
   - Appear in `tools/list` and can be called directly
   - NOT searchable via `tool_search` (in normal mode)
   - Changes (add/edit/remove) take effect immediately without server restart

2. **Ondemand Tools** (`visibility = "ondemand"`)
   - Registered globally at **server initialization**
   - Only discoverable via `tool_search`, not visible in `tools/list`
   - Changes require server restart to take effect

This hybrid approach provides the best of both worlds: commonly used native tools can be modified dynamically, while specialized ondemand tools are kept out of the tool list to avoid overwhelming the LLM.

### Tool Visibility Modes

The unified `/mcp` endpoint supports two modes via the `X-MCP-Tool-Mode` header or `tool_mode` query param:

**Normal mode** (default):

- Native tools appear in `tools/list` and can be called directly
- Native tools are NOT searchable via `tool_search`
- Ondemand tools are hidden from `tools/list` but discoverable via `tool_search`
- Both tool types are fully functional

**Discovery mode** (`X-MCP-Tool-Mode: discovery` or `?tool_mode=discovery`):

- All tools (native and ondemand) are hidden from `tools/list`
- All tools are only discoverable via `tool_search`
- Useful for AI clients that work better with fewer initial tools

### Dynamic Changes

#### Native Tool Changes (Fully Dynamic)

- ✅ Edit native tool scripts and changes take effect immediately
- ✅ Add new native tools - immediately discoverable
- ✅ Delete native tools - immediately removed
- ✅ Modify native tool definitions - changes picked up on next request
- ✅ No server restart required

#### Ondemand Tool Changes (Require Restart)

- ⚠️ Edit ondemand tool scripts - requires server restart
- ⚠️ Add new ondemand tools - requires server restart
- ⚠️ Delete ondemand tools - requires server restart
- ⚠️ Modify ondemand tool definitions - requires server restart

#### Library Changes

Custom libraries are loaded on-demand:

- ✅ Edit library files and changes take effect on next import
- ✅ No server restart required

### Technical Details

The hybrid approach is implemented in `mcp_server.go`:

```go
// HandleRequest handles HTTP requests to the MCP server in native mode.
// Native-visibility tools from providers appear in tools/list.
// OnDemand-visibility tools are searchable via tool_search but not listed.
func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	nativeProvider := NewNativeScriptToolProvider(m)
	onDemandProvider := NewOnDemandScriptToolProvider(m)

	// Start with native providers
	ctx := mcp.WithToolProviders(r.Context(), nativeProvider)

	// Add ondemand provider if there are any ondemand tools
	onDemandTools, _ := onDemandProvider.GetTools(r.Context())
	if len(onDemandTools) > 0 {
		ctx = mcp.WithOnDemandToolProviders(ctx, onDemandProvider)
	}

	m.server.HandleRequest(w, r.WithContext(ctx))
}
```

To enable discovery mode, clients can use the `X-MCP-Tool-Mode: discovery` header or `?tool_mode=discovery` query parameter. This is handled automatically by the MCP library.

Key insight: The MCP library's ToolProvider pattern ensures that provider tools are NOT searched by `tool_search` in normal mode (they only appear in `tools/list`). Only tools registered with `RegisterOnDemandTool()` are searchable via `tool_search`.

### When to Use Each Visibility

**Native (`"native"` or not set)** - Use for tools that are:

- Frequently used
- Part of core functionality
- Expected to be modified frequently
- Needed to be changeable without server restart

**OnDemand (`"ondemand"`)** - Use for tools that are:

- Specialized or experimental
- Used infrequently
- Part of a large toolset where showing all tools would be overwhelming
- Stable and don't need frequent changes

## Best Practices

### 1. Always Use llmr.mcp.return_string()

Explicitly return results for predictable behavior:

```python
# Good
llmr.mcp.return_string(f"Result: {value}")

# Avoid relying on implicit returns
print("This might work but is unpredictable")
```

### 2. Provide Default Values for Optional Parameters

```python
# Good
units = llmr.mcp.get("units", "celsius")
count = llmr.mcp.get("count", 10)

# Avoid
units = llmr.mcp.get("units")  # Returns None if not provided
```

### 3. Validate Required Parameters

```python
name = llmr.mcp.get("name")
if not name:
    llmr.mcp.return_string("Error: name parameter is required")
    return
```

### 4. Handle Errors Gracefully

```python
import llmr.mcp

try:
    # Perform operation
    result = risky_operation()
    llmr.mcp.return_string(f"Success: {result}")
except Exception as e:
    llmr.mcp.return_string(f"Error: {str(e)}")
```

### 5. Use Descriptive Keywords

Choose keywords that users might search for:

```toml
keywords = ["calculator", "math", "arithmetic", "add", "subtract", "multiply", "divide"]
```

### 6. Write Clear Descriptions

```toml
# Good
description = "Performs basic arithmetic operations (add, subtract, multiply, divide) on two numbers"

# Too vague
description = "Does math stuff"
```

## Complete Example: Calculator Tool

### tool.toml

```toml
name = "calculator"
description = "Performs basic arithmetic operations on two numbers"
keywords = ["calculator", "math", "arithmetic", "add", "subtract", "multiply", "divide"]
script = "calculator.py"

[parameters.operation]
type = "string"
description = "The operation to perform: add, subtract, multiply, or divide"
required = true

[parameters.a]
type = "number"
description = "The first number"
required = true

[parameters.b]
type = "number"
description = "The second number"
required = true
```

### calculator.py

```python
import llmr.mcp

def calculate(operation, a, b):
    """Perform the specified operation on two numbers."""
    operations = {
        "add": lambda x, y: x + y,
        "subtract": lambda x, y: x - y,
        "multiply": lambda x, y: x * y,
        "divide": lambda x, y: x / y if y != 0 else None,
    }

    if operation not in operations:
        return None, f"Unknown operation: {operation}"

    if operation == "divide" and b == 0:
        return None, "Cannot divide by zero"

    return operations[operation](a, b), None

def main():
    # Get parameters
    operation = llmr.mcp.get("operation")
    a = llmr.mcp.get("a")
    b = llmr.mcp.get("b")

    # Validate required parameters
    if not operation:
        llmr.mcp.return_string("Error: operation is required")
        return
    if a is None or b is None:
        llmr.mcp.return_string("Error: both a and b parameters are required")
        return

    # Perform calculation
    result, error = calculate(operation, a, b)

    if error:
        llmr.mcp.return_string(f"Error: {error}")
    else:
        llmr.mcp.return_string(str(result))

main()
```

## Testing Your Tool

### Via MCP Endpoint

```bash
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "execute_tool",
      "arguments": {
        "name": "calculator",
        "arguments": {
          "operation": "multiply",
          "a": 7,
          "b": 8
        }
      }
    }
  }'
```

For discovery mode, add the `X-MCP-Tool-Mode: discovery` header or use `?tool_mode=discovery` query param.

### Via CLI

```bash
./llmrouter tool calculator '{"operation": "add", "a": 5, "b": 3}'
```

### Discovering Your Tool

```bash
curl -X POST http://localhost:12345/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "tool_search",
      "arguments": {
        "query": "calculator"
      }
    }
  }'
```

For discovery mode, add the `X-MCP-Tool-Mode: discovery` header or use `?tool_mode=discovery` query param.

## Related Documentation

- [MCP Library Reference](mcp_library.md) - Full documentation for the `llmr.mcp` library
- [AI Library Reference](ai_library.md) - Full documentation for the `llmr.ai` library
