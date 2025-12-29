"""
MCP Demo Tool - Demonstrates MCP library functions

This tool shows how to use:
- mcp.list_tools() - List all available MCP tools
- mcp.tool_search(query) - Search for tools by keyword
- mcp.execute_tool(name, args) - Execute a discovered tool
- mcp.execute_script(code) - Execute arbitrary code
- mcp.call_tool(name, args) - Call any MCP tool directly
"""

import mcp
import json

# Get parameters
action = mcp.get("action", "list")
query = mcp.get("query", "")
args_str = mcp.get("args", "{}")

# Parse args if provided
try:
    args = json.loads(args_str) if args_str else {}
except:
    args = {}

result = []

if action == "list":
    # Demonstrate mcp.list_tools()
    result.append("=== MCP List Tools Demo ===\n")
    result.append("Using mcp.list_tools() to get all available tools:\n\n")

    tools = mcp.list_tools()
    for tool in tools:
        result.append("• " + tool['name'] + ": " + tool['description'] + "\n")

    result.append("\nTotal: " + str(len(tools)) + " tools available")

elif action == "search":
    # Demonstrate mcp.tool_search()
    result.append("=== MCP Tool Search Demo ===\n")
    result.append("Using mcp.tool_search(\"" + query + "\") to find matching tools:\n\n")

    if not query:
        result.append("Error: Please provide a 'query' parameter for search")
    else:
        matches = mcp.tool_search(query)
        if matches:
            for tool in matches:
                score = tool.get('score', 0)
                result.append("• " + tool['name'] + " (score: " + str(score) + ")\n")
                result.append("  " + tool['description'] + "\n\n")
            result.append("Found " + str(len(matches)) + " matching tools")
        else:
            result.append("No tools found matching your query")

elif action == "execute":
    # Demonstrate mcp.execute_tool()
    result.append("=== MCP Execute Tool Demo ===\n")
    result.append("Using mcp.execute_tool(\"" + query + "\", " + str(args) + ") to run a tool:\n\n")

    if not query:
        result.append("Error: Please provide a 'query' parameter with the tool name")
    else:
        try:
            output = mcp.execute_tool(query, args)
            result.append("Tool output:\n" + output)
        except Exception as e:
            result.append("Error executing tool: " + str(e))

elif action == "script":
    # Demonstrate mcp.execute_script()
    result.append("=== MCP Execute Script Demo ===\n")
    result.append("Using mcp.execute_script() to run arbitrary code:\n\n")

    # Example: Run a simple calculation script
    code = """
def fibonacci(n):
    if n <= 1:
        return n
    return fibonacci(n-1) + fibonacci(n-2)

print("Fibonacci sequence (first 10 numbers):")
for i in range(10):
    print("  fib(" + str(i) + ") = " + str(fibonacci(i)))
"""

    result.append("Code:\n" + code + "\n")
    result.append("Output:\n")

    try:
        output = mcp.execute_script(code)
        result.append(output)
    except Exception as e:
        result.append("Error: " + str(e))

elif action == "call":
    # Demonstrate mcp.call_tool() - direct MCP tool call
    result.append("=== MCP Call Tool Demo ===\n")
    result.append("Using mcp.call_tool(\"" + query + "\", " + str(args) + ") to call an MCP tool directly:\n\n")

    if not query:
        result.append("Error: Please provide a 'query' parameter with the tool name")
    else:
        try:
            output = mcp.call_tool(query, args)
            result.append("Tool output:\n" + output)
        except Exception as e:
            result.append("Error calling tool: " + str(e))

elif action == "full_demo":
    # Full demonstration of all functions
    result.append("=== Full MCP Library Demo ===\n\n")

    # 1. List tools
    result.append("1. Listing all tools with mcp.list_tools():\n")
    tools = mcp.list_tools()
    count = 0
    for tool in tools:
        if count < 5:
            result.append("   • " + tool['name'] + "\n")
        count = count + 1
    if len(tools) > 5:
        result.append("   ... and " + str(len(tools) - 5) + " more\n")

    # 2. Search for calculator
    result.append("\n2. Searching for 'calculator' with mcp.tool_search():\n")
    matches = mcp.tool_search("calculator")
    for tool in matches:
        score = tool.get('score', 0)
        result.append("   • " + tool['name'] + " (score: " + str(score) + ")\n")

    # 3. Execute calculator
    result.append("\n3. Executing calculator with mcp.execute_tool():\n")
    calc_result = mcp.execute_tool("calculator", {"operation": "multiply", "a": 7, "b": 6})
    result.append("   7 × 6 = " + calc_result + "\n")

    # 4. Execute script
    result.append("\n4. Running code with mcp.execute_script():\n")
    script_result = mcp.execute_script("print('Hello from execute_script!')")
    result.append("   " + script_result)

    result.append("\n\nDemo complete!")

else:
    result.append("Unknown action: " + action + "\n\n")
    result.append("Available actions:\n")
    result.append("  • list - List all available tools\n")
    result.append("  • search - Search for tools (requires 'query')\n")
    result.append("  • execute - Execute a tool (requires 'query' for name, optional 'args')\n")
    result.append("  • script - Demo execute_script with fibonacci\n")
    result.append("  • call - Call an MCP tool directly (requires 'query' for name)\n")
    result.append("  • full_demo - Run a full demonstration of all functions")

mcp.return_string("".join(result))
