# MCP Library for Scriptling

The MCP library provides functions for interacting with the Model Context Protocol (MCP) server within Scriptling scripts. It handles parameter passing, result returning, and tool operations.

## Quick Reference

| Function | Description |
|----------|-------------|
| `llmr.mcp.get(param_name, default=None)` | Get a parameter value passed to the tool |
| `llmr.mcp.return_string(text)` | Return a string result from the tool |
| `llmr.mcp.return_object(obj)` | Return an object as JSON from the tool |
| `llmr.mcp.return_toon(obj)` | Return an object as toon encoded string from the tool |
| `llmr.mcp.list_tools()` | List all MCP tools |
| `llmr.mcp.call_tool(name, args)` | Call an MCP tool directly (use `namespace/toolname` for namespaced tools) |
| `llmr.mcp.tool_search(query)` | Search for tools by keyword |
| `llmr.mcp.execute_tool(name, args)` | Execute a discovered tool (use `namespace/toolname` for namespaced tools) |
| `llmr.mcp.execute_code(code)` | Execute arbitrary script code |
| `llmr.mcp.toon_encode(obj)` | Encode an object to toon string |
| `llmr.mcp.toon_decode(str)` | Decode a toon string to object |

## Importing

```python
import llmr.mcp
```

## Parameter Functions

### llmr.mcp.get(param_name, default=None)

Gets a parameter value that was passed to the tool.

**Parameters:**
- `param_name` (string): The name of the parameter to retrieve
- `default` (optional): A default value to return if the parameter is not found

**Returns:**
- The parameter value (converted to appropriate Python type), the default value, or `None` if not found and no default provided

**Example:**
```python
import llmr.mcp

# Get a required parameter
name = llmr.mcp.get("name")

# Get an optional parameter with default
units = llmr.mcp.get("units", "celsius")

# Get a numeric parameter
count = llmr.mcp.get("count", 10)
```

## Return Functions

### llmr.mcp.return_string(text)

Sets the return value for the tool execution as a string. This should be called before the script ends to properly return the result to the MCP caller.

**Parameters:**
- `text` (string): The result text to return

**Returns:**
- The same string that was passed in

**Example:**
```python
import llmr.mcp

result = "Hello, World!"
llmr.mcp.return_string(result)
```

### llmr.mcp.return_object(obj)

Sets the return value for the tool execution as any object, converted to JSON.

**Parameters:**
- `obj` (any): The object to return (will be JSON serialized)

**Returns:**
- JSON string representation of the object

**Example:**
```python
import llmr.mcp

data = {"status": "success", "count": 42, "items": [1, 2, 3]}
llmr.mcp.return_object(data)
# Returns: {"count":42,"items":[1,2,3],"status":"success"}
```

### llmr.mcp.return_toon(obj)

Sets the return value for the tool execution as any object, converted to toon encoded string.

**Parameters:**
- `obj` (any): The object to return (will be toon serialized)

**Returns:**
- Toon string representation of the object

**Example:**
```python
import llmr.mcp

data = {"status": "success", "count": 42, "items": [1, 2, 3]}
llmr.mcp.return_toon(data)
# Returns: toon encoded string
```

## Toon Functions

### llmr.mcp.toon_encode(obj)

Encodes an object to a toon string.

**Parameters:**
- `obj` (any): The object to encode

**Returns:**
- Toon encoded string representation of the object

**Example:**
```python
import llmr.mcp

data = {"status": "success", "count": 42}
encoded = llmr.mcp.toon_encode(data)
print(encoded)  # toon encoded string
```

### llmr.mcp.toon_decode(str)

Decodes a toon string back to an object.

**Parameters:**
- `str` (string): The toon encoded string to decode

**Returns:**
- The decoded object (dict, list, string, number, etc.)

**Example:**
```python
import llmr.mcp

encoded = "toon encoded string"
decoded = llmr.mcp.toon_decode(encoded)
print(decoded)  # {"status": "success", "count": 42}
```

## Tool Functions

### llmr.mcp.list_tools()

Returns a list of all available tools registered with the MCP server.

**Returns:**
- A list of dictionaries with `name` and `description` keys

**Example:**
```python
import llmr.mcp

tools = llmr.mcp.list_tools()
for tool in tools:
    print(f"{tool['name']}: {tool['description']}")
```

### llmr.mcp.call_tool(name, args)

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
import llmr.mcp

# Call the calculator tool directly
result = llmr.mcp.call_tool("calculator", {
    "operation": "multiply",
    "a": 7,
    "b": 8
})
print(result)  # Outputs: 56.0

# JSON responses are automatically parsed
data = llmr.mcp.call_tool("get_data", {"id": 123})
print(data["status"])  # Access as dict if JSON is returned
print(data["count"])   # Works with parsed objects

# Multiple content blocks returned as list
blocks = llmr.mcp.call_tool("multi_output", {})
for block in blocks:
    print(block)
```

### llmr.mcp.tool_search(query)

Searches for tools by name, description, or keywords using the discovery system. This is a helper that wraps the `tool_search` MCP tool.

**Parameters:**
- `query` (string): The search query

**Returns:**
- A list of matching tools with `name`, `description`, and `score` keys. Namespaced tools will have names like `namespace/toolname`.

**Example:**
```python
import llmr.mcp

# Search for calculator-related tools
results = llmr.mcp.tool_search("calculator")
for tool in results:
    print(f"{tool['name']} (score: {tool['score']})")
    # Namespaced tools will appear as "namespace/toolname"
```

### llmr.mcp.execute_tool(name, args)

Executes a tool via the discovery system. This is a helper that wraps the `execute_tool` MCP tool.

**Parameters:**
- `name` (string): The name of the tool to execute. Use `namespace/toolname` format for namespaced tools.
- `args` (dict): A dictionary of arguments to pass to the tool

**Returns:**
- The tool's response, automatically decoded:
  - **Single text response**: Returns as a string
  - **JSON in text**: Automatically parsed and returned as objects (dict/list)
  - **Multiple content blocks**: Returns as a list of decoded blocks
  - **Image/Resource blocks**: Returns as a dict with `Type`, `Data`, `MimeType`, etc.

**Example:**
```python
import llmr.mcp

# Search for a tool, then execute it
results = llmr.mcp.tool_search("weather")
if results:
    tool_name = results[0]['name']
    # tool_name may be "namespace/toolname" for namespaced tools
    result = llmr.mcp.execute_tool(tool_name, {"city": "London"})
    # result is decoded - could be string, dict, or list
    print(result)

# Execute a namespaced tool directly
result = llmr.mcp.execute_tool("math/calculator", {"expression": "2+2"})
# If result is JSON like {"answer": 4}, it's already parsed
print(result)  # {"answer": 4}
print(result["answer"])  # 4
```

### llmr.mcp.execute_code(code)

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
import llmr.mcp

# Execute a simple calculation
result = llmr.mcp.execute_code("print(2 + 2)")
print(result)  # Outputs: 4

# Execute more complex code
code = '''
def factorial(n):
    if n <= 1:
        return 1
    return n * factorial(n - 1)

print(factorial(5))
'''
result = llmr.mcp.execute_code(code)
print(result)  # Outputs: 120

# Code that returns JSON is automatically parsed
json_code = '''
import json
print(json.dumps({"status": "ok", "count": 42}))
'''
result = llmr.mcp.execute_code(json_code)
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
import llmr.mcp

def get_greeting(name, style):
    """Generate a greeting based on style"""
    if style == "formal":
        return f"Good day, {name}. It is a pleasure to make your acquaintance."
    elif style == "enthusiastic":
        return f"Hey {name}!!! SO GREAT to see you! 🎉"
    else:  # casual (default)
        return f"Hi {name}, nice to meet you!"

# Get parameters
name = llmr.mcp.get("name")
style = llmr.mcp.get("style", "casual")

# Validate required parameter
if not name:
    llmr.mcp.return_string("Error: name parameter is required")
else:
    result = get_greeting(name, style)
    llmr.mcp.return_string(result)
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

The MCP library automatically decodes tool responses for easier use in scripts. When you call `llmr.mcp.call_tool()`, `llmr.mcp.execute_tool()`, or `llmr.mcp.execute_code()`, the response is intelligently decoded:

### Single Text Response

Returns as a string:
```python
result = llmr.mcp.call_tool("some_tool", {})
print(result)  # "Hello, World!"
```

### JSON in Text

Automatically parsed as dict or list:
```python
# Tool returns: '{"status": "ok", "count": 42}'
result = llmr.mcp.call_tool("status_tool", {})
print(result["status"])  # "ok"
print(result["count"])   # 42
```

### Multiple Content Blocks

Returns as a list of decoded blocks:
```python
blocks = llmr.mcp.call_tool("multi_tool", {})
for block in blocks:
    print(block)  # Each decoded block
```

### Image/Resource Blocks

Returns as a dict with metadata:
```python
image = llmr.mcp.call_tool("image_tool", {})
print(image["Type"])      # "image"
print(image["MimeType"])  # "image/png"
print(image["Data"])      # base64 data
```

### Checking Response Type

You can check the type of response you received:
```python
result = llmr.mcp.call_tool("some_tool", {})

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
import llmr.mcp
import llmr.ai

# Get the question from parameters
question = llmr.mcp.get("question")

# Use AI to answer
messages = [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": question}
]

response = llmr.ai.completion("mistralai/devstral-small-2-2512", messages)

# Return the AI response
llmr.mcp.return_string(response)
```

## Function Comparison

| MCP Tool | Library Helper | Use Case |
|----------|---------------|----------|
| `tool_search` | `llmr.mcp.tool_search(query)` | Find tools by keyword |
| `execute_tool` | `llmr.mcp.execute_tool(name, args)` | Execute discovered tools |
| `execute_code` | `llmr.mcp.execute_code(code)` | Run arbitrary code |
| (direct) | `llmr.mcp.call_tool(name, args)` | Call any MCP tool directly |
| (direct) | `llmr.mcp.list_tools()` | List all available tools |

## Return Value Behavior

- If `llmr.mcp.return_string()`, `llmr.mcp.return_object()`, or `llmr.mcp.return_toon()` is called, that value is used as the tool's response
- If none are called, the script's captured output (via `print()`) and/or final expression result is used
- Always prefer using `llmr.mcp.return_string()` for explicit, predictable results

## Best Practices

1. **Always call a return function**: Explicitly return results using `llmr.mcp.return_string()`, `llmr.mcp.return_object()`, or `llmr.mcp.return_toon()` for predictable behavior
2. **Provide defaults for optional parameters**: Use `llmr.mcp.get("param", default)` pattern
3. **Validate required parameters**: Check if required parameters exist before using them
4. **Use appropriate types**: Match parameter types to your tool's needs
5. **Handle errors gracefully**: Return error messages via llmr.mcp.return_string() rather than raising exceptions

## Error Handling Example

```python
import llmr.mcp

try:
    # Get and validate parameters
    value = llmr.mcp.get("value")
    if value is None:
        raise ValueError("value parameter is required")

    # Perform operation
    result = process_value(value)
    llmr.mcp.return_string(f"Success: {result}")

except Exception as e:
    llmr.mcp.return_string(f"Error: {str(e)}")
```

## Using Dynamic Libraries

Tools can import custom libraries from the `libraries_path` directory:

```python
import llmr.mcp
import string_utils  # Loaded from libraries_path/string_utils.py

text = llmr.mcp.get("text")
result = string_utils.to_uppercase(text)
llmr.mcp.return_string(result)
```

Libraries are loaded on-demand when first imported. Simply create a `.py` file in the configured libraries directory.

## See Also

- [AI Library Reference](ai_library.md) - For LLM completions
- [Creating Custom Tools](creating_tools.md) - Guide to creating MCP tools
