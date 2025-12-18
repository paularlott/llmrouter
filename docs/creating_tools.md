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

| Field | Description | Required |
|-------|-------------|----------|
| `name` | Tool identifier (used in API calls). Defaults to directory name if not specified. | No |
| `description` | Human-readable description shown in tool discovery. | Yes |
| `keywords` | Array of keywords for tool search/discovery. | No |
| `script` | Filename of the script to execute (relative to tool directory). | Yes |
| `parameters` | Map of parameter definitions. | No |

### Parameter Types

| Type | Description | Python Type |
|------|-------------|-------------|
| `string` | Text values | `str` |
| `number` | Numeric values (integers or floats) | `float` |
| `boolean` | True/false values | `bool` |
| `array` | List of values | `list` |

### Parameter Properties

| Property | Description | Default |
|----------|-------------|---------|
| `type` | Parameter type (string, number, boolean, array) | Required |
| `description` | Human-readable description | Required |
| `required` | Whether the parameter must be provided | `false` |

## Tool Script

Tool scripts are written in Python/Scriptling and use the `mcp` and `ai` libraries.

### Basic Tool Example

```python
import mcp

def main():
    # Get parameters
    name = mcp.get("name")
    count = mcp.get("count", 1)  # Default value of 1
    uppercase = mcp.get("uppercase", False)

    # Validate required parameter
    if not name:
        mcp.return_string("Error: name parameter is required")
        return

    # Build result
    greeting = f"Hello, {name}!"
    if uppercase:
        greeting = greeting.upper()

    result = "\n".join([greeting] * int(count))

    # Return result
    mcp.return_string(result)

main()
```

### Tool with AI Integration

Tools can use the AI library to interact with LLMs:

```python
import mcp
import ai

def main():
    question = mcp.get("question")
    model = mcp.get("model", "mistralai/devstral-small-2-2512")

    if not question:
        mcp.return_string("Error: question parameter is required")
        return

    # Create messages for the AI
    messages = [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": question}
    ]

    # Get AI completion (automatic tool calling is handled internally)
    response = ai.completion(model, messages)

    mcp.return_string(response)

main()
```

### Using Custom Libraries

Tools can import custom libraries from the `libraries_path` directory:

```python
import mcp
import string_utils  # Loaded from libraries_path/string_utils.py

def main():
    text = mcp.get("text")
    result = string_utils.to_uppercase(text)
    mcp.return_string(result)

main()
```

## Dynamic Loading

All aspects of scripted tools are now **fully dynamic**:

### Script Changes
Tool scripts are loaded from disk on each execution:
- ✅ Edit scripts and changes take effect immediately
- ✅ No server restart required

### New Tools
New tools are discovered automatically:
- ✅ Create a new tool directory with `tool.toml` and script
- ✅ The tool is immediately discoverable via `tool_search`
- ✅ No server restart required

### Removed Tools
Removed tools are no longer available:
- ✅ Delete a tool directory
- ✅ The tool is immediately removed from discovery
- ✅ No server restart required

### Edited Tool Definitions
Changes to `tool.toml` files take effect immediately:
- ✅ Modify tool name, description, parameters, or keywords
- ✅ Changes are picked up on the next `tool_search` or `execute_tool` call
- ✅ No server restart required

### Library Changes
Custom libraries are loaded on-demand:
- ✅ Edit library files and changes take effect on next import
- ✅ No server restart required

## Best Practices

### 1. Always Use mcp.return_string()

Explicitly return results for predictable behavior:

```python
# Good
mcp.return_string(f"Result: {value}")

# Avoid relying on implicit returns
print("This might work but is unpredictable")
```

### 2. Provide Default Values for Optional Parameters

```python
# Good
units = mcp.get("units", "celsius")
count = mcp.get("count", 10)

# Avoid
units = mcp.get("units")  # Returns None if not provided
```

### 3. Validate Required Parameters

```python
name = mcp.get("name")
if not name:
    mcp.return_string("Error: name parameter is required")
    return
```

### 4. Handle Errors Gracefully

```python
import mcp

try:
    # Perform operation
    result = risky_operation()
    mcp.return_string(f"Success: {result}")
except Exception as e:
    mcp.return_string(f"Error: {str(e)}")
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
import mcp

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
    operation = mcp.get("operation")
    a = mcp.get("a")
    b = mcp.get("b")

    # Validate required parameters
    if not operation:
        mcp.return_string("Error: operation is required")
        return
    if a is None or b is None:
        mcp.return_string("Error: both a and b parameters are required")
        return

    # Perform calculation
    result, error = calculate(operation, a, b)

    if error:
        mcp.return_string(f"Error: {error}")
    else:
        mcp.return_string(str(result))

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

## Related Documentation

- [MCP Library Reference](mcp_library.md) - Full documentation for the `mcp` library
- [AI Library Reference](ai_library.md) - Full documentation for the `ai` library
