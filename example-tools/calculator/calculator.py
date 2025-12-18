# Import MCP library for proper result handling
import mcp

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

# Use mcp.get() to access required parameters
operation = mcp.get("operation")
a = mcp.get("a")
b = mcp.get("b")
result = calculate(operation, a, b)

# Return the result using MCP library
mcp.return_string(str(result))