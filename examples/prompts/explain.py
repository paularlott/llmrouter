import scriptling.mcp.tool as tool

concept = tool.get_string("concept")
level = tool.get_string("level", "simple")

if level == "advanced":
    instruction = "Provide a technically detailed explanation of"
elif level == "intermediate":
    instruction = "Explain"
else:
    instruction = "Explain in simple terms with analogies"

tool.return_object({
    "messages": [
        {"role": "user", "content": instruction + " " + concept + "."}
    ]
})
