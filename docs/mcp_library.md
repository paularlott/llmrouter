# MCP Library for Scriptling

The MCP library provides functions for interacting with the Model Context Protocol (MCP) server within Scriptling scripts. It handles parameter passing, result returning, and tool operations.

## Quick Reference

| Function | Description |
|----------|-------------|
| `mcp.get(param_name, default=None)` | Get a parameter value passed to the tool |
| `mcp.return_string(text)` | Return a string result from the tool |
| `mcp.return_object(obj)` | Return an object as JSON from the tool |
| `mcp.list_tools()` | List all MCP tools |
| `mcp.call_tool(name, args)` | Call an MCP tool directly |
| `mcp.tool_search(query)` | Search for tools using discovery |
| `mcp.execute_tool(name, args)` | Execute a tool via discovery |
| `mcp.execute_code(code)` | Execute arbitrary script code |

## Importing

```python
import mcp
```

## Parameter Functions

### mcp.get(param_name, default=None)

Gets a parameter value that was passed to the tool.

**Parameters:**
- `param_name` (string): The name of the parameter to retrieve
- `default` (optional): A default value to return if the parameter is not found

**Returns:**
- The parameter value (converted to appropriate Python type), the default value, or `None` if not found and no default provided

**Example:**
```python
import mcp

# Get a required parameter
name = mcp.get("name")

# Get an optional parameter with default
units = mcp.get("units", "celsius")

# Get a numeric parameter
count = mcp.get("count", 10)
```

## Return Functions

### mcp.return_string(text)

Sets the return value for the tool execution as a string. This should be called before the script ends to properly return the result to the MCP caller.

**Parameters:**
- `text` (string): The result text to return

**Returns:**
- The same string that was passed in

**Example:**
```python
import mcp

result = "Hello, World!"
mcp.return_string(result)
```

### mcp.return_object(obj)

Sets the return value for the tool execution as any object, converted to JSON.

**Parameters:**
- `obj` (any): The object to return (will be JSON serialized)

**Returns:**
- JSON string representation of the object

**Example:**
```python
import mcp

data = {"status": "success", "count": 42, "items": [1, 2, 3]}
mcp.return_object(data)
# Returns: {"count":42,"items":[1,2,3],"status":"success"}
```

## Tool Functions

### mcp.list_tools()

Returns a list of all available tools registered with the MCP server.

**Returns:**
- A list of dictionaries with `name` and `description` keys

**Example:**
```python
import mcp

tools = mcp.list_tools()
for tool in tools:
    print(f"{tool['name']}: {tool['description']}")
```

### mcp.call_tool(name, args)

Calls an MCP tool directly by name with the provided arguments.

**Parameters:**
- `name` (string): The name of the tool to call
- `args` (dict): A dictionary of arguments to pass to the tool

**Returns:**
- The tool's response, automatically decoded:
  - **Single text response**: Returns as a string
  - **JSON in text**: Automatically parsed and returned as objects (dict/list)
  - **Multiple content blocks**: Returns as a list of decoded blocks
  - **Image/Resource blocks**: Returns as a dict with `Type`, `Data`, `MimeType`, etc.

**Example:**
```python
import mcp

# Call the calculator tool directly
result = mcp.call_tool("calculator", {
    "operation": "multiply",
    "a": 7,
    "b": 8
})
print(result)  # Outputs: 56.0

# JSON responses are automatically parsed
data = mcp.call_tool("get_data", {"id": 123})
print(data["status"])  # Access as dict if JSON is returned
print(data["count"])   # Works with parsed objects

# Multiple content blocks returned as list
blocks = mcp.call_tool("multi_output", {})
for block in blocks:
    print(block)
```

### mcp.tool_search(query, namespace?)

Searches for tools by name, description, or keywords using the discovery system. This is a helper that wraps the `tool_search` MCP tool.

**Parameters:**
- `query` (string): The search query
- `namespace` (string, optional): If provided, calls the MCP tool `<namespace>/tool_search` instead of `tool_search`

**Returns:**
- A list of matching tools with `name`, `description`, and `score` keys

**Example:**
```python
import mcp

# Search for calculator-related tools using default tool_search
results = mcp.tool_search("calculator")
for tool in results:
    print(f"{tool['name']} (score: {tool['score']})")

# Search using a namespaced tool_search tool
results = mcp.tool_search("calculator", "math")
for tool in results:
    print(f"{tool['name']} (score: {tool['score']})")  # Uses "math/tool_search"
```

### mcp.execute_tool(name, args, namespace?)

Executes a tool via the discovery system. This is a helper that wraps the `execute_tool` MCP tool.

**Parameters:**
- `name` (string): The name of the tool to execute
- `args` (dict): A dictionary of arguments to pass to the tool
- `namespace` (string, optional): If provided, calls the MCP tool `<namespace>/execute_tool` instead of `execute_tool`

**Returns:**
- The tool's response, automatically decoded:
  - **Single text response**: Returns as a string
  - **JSON in text**: Automatically parsed and returned as objects (dict/list)
  - **Multiple content blocks**: Returns as a list of decoded blocks
  - **Image/Resource blocks**: Returns as a dict with `Type`, `Data`, `MimeType`, etc.

**Example:**
```python
import mcp

# Search for a tool, then execute it
results = mcp.tool_search("weather")
if results:
    tool_name = results[0]['name']
    result = mcp.execute_tool(tool_name, {"city": "London"})
    # result is decoded - could be string, dict, or list
    print(result)

# Execute a tool using a namespaced execute_tool
result = mcp.execute_tool("calculator", {"expression": "2+2"}, "math")
# Uses "math/execute_tool" to execute "calculator"
# If result is JSON like {"answer": 4}, it's already parsed
print(result)  # {"answer": 4}
print(result["answer"])  # 4
```

### mcp.execute_code(code)

Executes arbitrary Scriptling/Python code. This is a helper that wraps the `execute_code` MCP tool.

**Parameters:**
- `code` (string): The code to execute

**Returns:**
- The script's output, automatically decoded:
  - **Single text response**: Returns as a string
  - **JSON in text**: Automatically parsed and returned as objects (dict/list)
  - **Multiple content blocks**: Returns as a list of decoded blocks

**Example:**
```python
import mcp

# Execute a simple calculation
result = mcp.execute_code("print(2 + 2)")
print(result)  # Outputs: 4

# Execute more complex code
code = '''
def factorial(n):
    if n <= 1:
        return 1
    return n * factorial(n - 1)

print(factorial(5))
'''
result = mcp.execute_code(code)
print(result)  # Outputs: 120

# Code that returns JSON is automatically parsed
json_code = '''
import json
print(json.dumps({"status": "ok", "count": 42}))
'''
result = mcp.execute_code(json_code)
print(result["status"])  # "ok"
print(result["count"])   # 42
```

## Complete Tool Example

Here's a complete example of a tool that uses the MCP library:

### tool.toml
```toml
name = "greeting_tool"
description = "A tool that generates personalized greetings"
keywords = ["greeting", "hello", "welcome", "personalize"]
script = "greet.py"

[parameters.name]
type = "string"
description = "The name of the person to greet"
required = true

[parameters.style]
type = "string"
description = "The greeting style: formal, casual, or enthusiastic"
required = false
```

### greet.py
```python
import mcp

def get_greeting(name, style):
    """Generate a greeting based on style"""
    if style == "formal":
        return f"Good day, {name}. It is a pleasure to make your acquaintance."
    elif style == "enthusiastic":
        return f"Hey {name}!!! SO GREAT to see you! 🎉"
    else:  # casual (default)
        return f"Hi {name}, nice to meet you!"

# Get parameters
name = mcp.get("name")
style = mcp.get("style", "casual")

# Validate required parameter
if not name:
    mcp.return_string("Error: name parameter is required")
else:
    result = get_greeting(name, style)
    mcp.return_string(result)
```

## Parameter Types

When parameters are passed to a tool, they are automatically converted to appropriate Python types:

| TOML Type | Python Type |
|-----------|-------------|
| `string`  | `str`       |
| `number`  | `float`     |
| `boolean` | `bool`      |
| `array`   | `list`      |

## Response Decoding

The MCP library automatically decodes tool responses for easier use in scripts. When you call `mcp.call_tool()`, `mcp.execute_tool()`, or `mcp.execute_code()`, the response is intelligently decoded:

### Single Text Response

Returns as a string:
```python
result = mcp.call_tool("some_tool", {})
print(result)  # "Hello, World!"
```

### JSON in Text

Automatically parsed as dict or list:
```python
# Tool returns: '{"status": "ok", "count": 42}'
result = mcp.call_tool("status_tool", {})
print(result["status"])  # "ok"
print(result["count"])   # 42
```

### Multiple Content Blocks

Returns as a list of decoded blocks:
```python
blocks = mcp.call_tool("multi_tool", {})
for block in blocks:
    print(block)  # Each decoded block
```

### Image/Resource Blocks

Returns as a dict with metadata:
```python
image = mcp.call_tool("image_tool", {})
print(image["Type"])      # "image"
print(image["MimeType"])  # "image/png"
print(image["Data"])      # base64 data
```

### Checking Response Type

You can check the type of response you received:
```python
result = mcp.call_tool("some_tool", {})

if isinstance(result, str):
    print("Got string:", result)
elif isinstance(result, dict):
    print("Got dict:", result.keys())
elif isinstance(result, list):
    print("Got list with", len(result), "items")
```

## Using with AI Library

The MCP and AI libraries work together seamlessly:

```python
import mcp
import ai

# Get the question from parameters
question = mcp.get("question")

# Use AI to answer
messages = [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": question}
]

response = ai.completion("mistralai/devstral-small-2-2512", messages)

# Return the AI response
mcp.return_string(response)
```

## Function Comparison

| MCP Tool | Library Helper | Use Case |
|----------|---------------|----------|
| `tool_search` | `mcp.tool_search(query)` | Find tools by keyword |
| `execute_tool` | `mcp.execute_tool(name, args)` | Execute discovered tools |
| `execute_code` | `mcp.execute_code(code)` | Run arbitrary code |
| (direct) | `mcp.call_tool(name, args)` | Call any MCP tool directly |
| (direct) | `mcp.list_tools()` | List all available tools |

## Return Value Behavior

- If `mcp.return_string()` or `mcp.return_object()` is called, that value is used as the tool's response
- If neither is called, the script's captured output (via `print()`) and/or final expression result is used
- Always prefer using `mcp.return_string()` for explicit, predictable results

## Best Practices

1. **Always call mcp.return_string()**: Explicitly return results for predictable behavior
2. **Provide defaults for optional parameters**: Use `mcp.get("param", default)` pattern
3. **Validate required parameters**: Check if required parameters exist before using them
4. **Use appropriate types**: Match parameter types to your tool's needs
5. **Handle errors gracefully**: Return error messages via mcp.return_string() rather than raising exceptions

## Error Handling Example

```python
import mcp

try:
    # Get and validate parameters
    value = mcp.get("value")
    if value is None:
        raise ValueError("value parameter is required")

    # Perform operation
    result = process_value(value)
    mcp.return_string(f"Success: {result}")

except Exception as e:
    mcp.return_string(f"Error: {str(e)}")
```

## Using Dynamic Libraries

Tools can import custom libraries from the `libraries_path` directory:

```python
import mcp
import string_utils  # Loaded from libraries_path/string_utils.py

text = mcp.get("text")
result = string_utils.to_uppercase(text)
mcp.return_string(result)
```

Libraries are loaded on-demand when first imported. Simply create a `.py` file in the configured libraries directory.

## See Also

- [AI Library Reference](ai_library.md) - For LLM completions
- [Creating Custom Tools](creating_tools.md) - Guide to creating MCP tools
