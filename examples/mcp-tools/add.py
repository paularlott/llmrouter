import scriptling.mcp.tool as tool

a = tool.get_int("a")
b = tool.get_int("b")

result = a + b
tool.return_string(f"{a} + {b} = {result}")
