import scriptling.mcp.tool as tool

# Get parameters
name = tool.get_string("name", "World")
times = tool.get_int("times", 1)

# Generate greeting
greetings = []
for i in range(times):
    greetings.append(f"Hello, {name}!")

result = "\n".join(greetings)

# Return result
tool.return_string(result)
