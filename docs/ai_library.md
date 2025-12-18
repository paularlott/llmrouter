# AI Library for Scriptling

The AI library provides LLM completion capabilities within Scriptling scripts. It allows scripts to interact with LLM models configured in the LLMRouter, with optional automatic tool execution support.

## Quick Reference

| Function | Description |
|----------|-------------|
| `ai.completion(model, messages, tools=True)` | Create a chat completion with optional tool calling |

## Importing

```python
import ai
```

## Functions

### ai.completion(model, messages, tools=True)

Creates a chat completion with the specified model. When `tools=True` (default), automatically handles tool calls from the LLM, executing them via the MCP server and returning the final response.

**Parameters:**
- `model` (string): The name of the model to use (must be available via one of the configured providers)
- `messages` (list): A list of message dictionaries with `role` and `content` keys
- `tools` (boolean, optional): Enable automatic tool calling. Default is `True`. Set to `False` for simple completions without tool access.

**Returns:**
- A string containing the model's response

**Example:**
```python
import ai

# Simple completion without tools (faster, no tool loop risk)
messages = [
    {"role": "user", "content": "What is the capital of France?"}
]
response = ai.completion("gemini-2.0-flash-lite", messages, tools=False)
print(response)

# Completion with tool calling enabled (default)
messages = [
    {"role": "system", "content": "You have access to tools. Use tool_search to find tools."},
    {"role": "user", "content": "Calculate 15 * 23 using the calculator tool"}
]
response = ai.completion("mistralai/devstral-small-2-2512", messages, tools=True)
print(response)

# With system message, no tools
messages = [
    {"role": "system", "content": "You are a helpful math assistant."},
    {"role": "user", "content": "Calculate 15% of 200"}
]
response = ai.completion("mistralai/devstral-small-2-2512", messages, tools=False)
print(response)
```

## Automatic Tool Calling

When using `ai.completion()`, the AI library automatically:

1. Provides `tool_search` and `execute_tool` functions to the LLM
2. Executes any tool calls the LLM requests
3. Returns the tool results to the LLM
4. Continues the conversation until the LLM provides a final response

This process is limited to a maximum of 10 iterations to prevent infinite loops.

### How It Works

1. The script calls `ai.completion()` with a model and messages
2. The LLM may respond with a tool call (e.g., `tool_search` to find relevant tools)
3. The AI library executes the tool and adds the result to the conversation
4. The LLM processes the tool result and may make additional tool calls
5. Eventually, the LLM provides a final text response
6. This response is returned to the script

### Example with Tool Usage

```python
import ai
import mcp

def ask_question(question):
    """Ask a question that may require tool usage"""
    messages = [
        {"role": "system", "content": """You are a helpful assistant with access to tools.
Use tool_search to find available tools, and execute_tool to run them.
Answer questions by using the appropriate tools when needed."""},
        {"role": "user", "content": question}
    ]

    response = ai.completion("mistralai/devstral-small-2-2512", messages)
    return response

# Ask a question that requires calculation
result = ask_question("What is 15 multiplied by 23?")
print(result)

# Return the result
mcp.return_string(result)
```

## Available Models

The available models depend on your LLMRouter configuration. Check your `config.toml` for configured providers and their supported models. Common configurations include:

- Local LLM servers (e.g., LM Studio, Ollama)
- OpenAI API
- Google Gemini (via OpenAI-compatible endpoint)
- Azure OpenAI

## Error Handling

The AI library will raise an error if:

- The specified model is not available
- The maximum tool call iterations (10) are exceeded
- Network or provider errors occur

**Example with error handling:**
```python
import ai
import mcp

try:
    response = ai.completion(
        "invalid-model-name",
        [{"role": "user", "content": "Hello!"}]
    )
    mcp.return_string(response)
except Exception as e:
    mcp.return_string(f"Error: {str(e)}")
```

## Best Practices

1. **Use appropriate system prompts**: Guide the LLM on when and how to use tools
2. **Handle errors gracefully**: Wrap AI calls in try-except blocks
3. **Choose the right model**: Consider cost, speed, and capability when selecting models
4. **Keep conversations focused**: Clear, specific prompts lead to better results
5. **Use mcp.return_string()**: Always return results properly using the MCP library

## See Also

- [MCP Library Reference](mcp_library.md) - For tool operations (`mcp.call_tool`, `mcp.tool_search`, etc.)
