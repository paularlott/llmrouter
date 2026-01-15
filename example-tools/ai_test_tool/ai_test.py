import llmr.ai
import llmr.mcp

# Get the question from parameters
question = llmr.mcp.get("question")
model = llmr.mcp.get("model", "mistralai/devstral-small-2-2512")

print("Question: " + str(question))
print("Model: " + str(model))

# Build messages with a system prompt guiding appropriate tool usage
messages = [
    {"role": "system", "content": """You are a helpful assistant with access to tools.
Use tool_search to find tools, and execute_tool to run them."""},
    {"role": "user", "content": question}
]

response = llmr.ai.completion(model, messages)

llmr.mcp.return_string("AI Response: " + str(response))