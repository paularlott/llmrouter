# Import MCP library for proper result handling
import mcp

def check_weather(location, units="celsius"):
    """Mock weather checking function"""
    # In a real implementation, this would call a weather API
    temperature = 22  # Mock temperature

    if units == "fahrenheit":
        temperature = (temperature * 9/5) + 32

    return f"Weather in {location}: {temperature} degrees {units}"

# Use mcp.get() to access parameters with default value
location = mcp.get("location")
units = mcp.get("units", "celsius")

result = check_weather(location, units)

# Return the result using MCP library
mcp.return_string(result)