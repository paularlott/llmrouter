# Import MCP library for proper result handling
import llmr.mcp

def greet(name):
    """Return a greeting message"""
    if name:
        return "Bonjour " + name + "!"
    return "Hello, World!"

# Use llmr.mcp.get() to access parameters with default value
name = llmr.mcp.get("name", "World")
result = greet(name)

# Return the result using MCP library
llmr.mcp.return_string(result)