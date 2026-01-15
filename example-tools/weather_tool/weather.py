# Import MCP library for proper result handling
import llmr.mcp

def check_weather(location, units="celsius"):
    """Mock weather checking function"""
    # In a real implementation, this would call a weather API
    temperature = 22  # Mock temperature

    if units == "fahrenheit":
        temperature = (temperature * 9/5) + 32

    return f"Weather in {location}: {temperature} degrees {units}"

# Use llmr.mcp.get() to access parameters with default value
location = llmr.mcp.get("location")
units = llmr.mcp.get("units", "celsius")

result = check_weather(location, units)

# Return the result using MCP library
llmr.mcp.return_string(result)