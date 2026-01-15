# Import MCP library for proper result handling
import llmr.mcp

def calculate(operation, a, b):
    """Perform the calculation with proper error handling"""
    a = float(a)
    b = float(b)

    if operation == "add":
        return a + b
    elif operation == "subtract":
        return a - b
    elif operation == "multiply":
        return a * b
    elif operation == "divide":
        if b == 0:
            return "Error: Division by zero"
        return a / b
    else:
        return "Error: Unknown operation"

# Use llmr.mcp.get() to access required parameters
operation = llmr.mcp.get("operation")
a = llmr.mcp.get("a")
b = llmr.mcp.get("b")
result = calculate(operation, a, b)

# Return the result using MCP library
llmr.mcp.return_string(str(result))