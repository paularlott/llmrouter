# AI Library for Scriptling

The AI library provides LLM completion capabilities within Scriptling scripts. It allows scripts to interact with LLM models configured in the LLMRouter, with optional automatic tool execution support.

## Quick Reference

| Function | Description |
|----------|-------------|
| `ai.completion(model, messages)` | Create a chat completion with automatic tool calling |
| `ai.embedding(model, input)` | Generate embeddings for text or list of texts |
| `ai.response_create(model, input, instructions=None, previous_response_id=None)` | Create a new response object for async processing with automatic tool calling |
| `ai.response_get(id)` | Retrieve a response by ID |
| `ai.response_delete(id)` | Delete a response by ID |
| `ai.response_cancel(id)` | Cancel an in-progress response |

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

### ai.embedding(model, input)

Generates embeddings for the given input using the specified embedding model.

**Parameters:**
- `model` (string): The name of the embedding model to use
- `input` (string or list): Text string or list of text strings to embed

**Returns:**
- A list of embedding vectors (each vector is a list of floats)

**Example:**
```python
import ai

# Single text embedding
embedding = ai.embedding("text-embedding-ada-002", "Hello world")
print(f"Embedding dimensions: {len(embedding[0])}")

# Multiple texts
texts = ["Hello world", "Goodbye world"]
embeddings = ai.embedding("text-embedding-ada-002", texts)
print(f"Generated {len(embeddings)} embeddings")
```

### ai.response_create(model, input, instructions=None, previous_response_id=None)

Creates a new response object for asynchronous processing. This follows the OpenAI Responses API pattern.

**Parameters:**
- `model` (string): The model to use for processing
- `input` (string or list): Input text or list of input texts to process
- `instructions` (string, optional): System instructions for the model
- `previous_response_id` (string, optional): ID of previous response to continue conversation

**Returns:**
- A string containing the response ID

**Example:**
```python
import ai

# Create a response with simple input
response_id = ai.response_create("gpt-4", "Hello, how are you?")
print(f"Created response: {response_id}")

# Create a response with instructions
response_id = ai.response_create(
    "gpt-4",
    "What is 2+2?",
    "You are a helpful math assistant."
)
print(f"Created response: {response_id}")

# Chain conversations using previous_response_id
first_response = ai.response_create("gpt-4", "Tell me a joke")
# Wait for completion, then continue
second_response = ai.response_create(
    "gpt-4",
    "Tell me another one",
    None,
    first_response  # Continue the conversation
)
print(f"Chained response: {second_response}")
```

### ai.response_get(id)

Retrieves a response object by its ID.

**Parameters:**
- `id` (string): The response ID to retrieve

**Returns:**
- A dictionary containing response details (id, status, model, content if completed)

**Example:**
```python
import ai

# Get response details
response = ai.response_get("resp_abc123")
print(f"Status: {response['status']}")
if 'content' in response:
    print(f"Content: {response['content']}")
```

### ai.response_delete(id)

Deletes a response by its ID.

**Parameters:**
- `id` (string): The response ID to delete

**Returns:**
- Boolean indicating success

**Example:**
```python
import ai

# Delete a response
success = ai.response_delete("resp_abc123")
print(f"Deleted: {success}")
```

### ai.response_cancel(id)

Cancels an in-progress response.

**Parameters:**
- `id` (string): The response ID to cancel

**Returns:**
- String containing the new status

**Example:**
```python
import ai

# Cancel a response
status = ai.response_cancel("resp_abc123")
print(f"New status: {status}")
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
