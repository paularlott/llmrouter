# Import MCP library for proper result handling
import mcp

def greet(name):
    """Return a greeting message"""
    if name:
        return "Bonjour " + name + "!"
    return "Hello, World!"

# Use mcp.get() to access parameters with default value
name = mcp.get("name", "World")
result = greet(name)

# Return the result using MCP library
mcp.return_string(result)