import scriptling.mcp.tool as tool

text = tool.get_string("text")
style = tool.get_string("style", "brief")

if style == "bullet-points":
    instruction = "Summarise the following text as a bulleted list of key points:"
elif style == "detailed":
    instruction = "Provide a detailed summary of the following text, preserving important details:"
else:
    instruction = "Provide a brief summary of the following text in 2-3 sentences:"

tool.return_object({
    "messages": [
        {"role": "user", "content": instruction + "\n\n" + text}
    ]
})
