package main

import (
	"context"
	"encoding/json"

	"github.com/paularlott/mcp/openai"
	"github.com/paularlott/scriptling/errors"
	"github.com/paularlott/scriptling/object"
)

// RouterInterface defines the interface needed by AILibrary
type RouterInterface interface {
	CreateChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

// AILibrary provides AI completion and tool calling capabilities
type AILibrary struct {
	router *Router
}

// NewAILibrary creates a new AI library instance
func NewAILibrary(router *Router) *AILibrary {
	return &AILibrary{
		router: router,
	}
}

// GetLibrary returns the scriptling library object for AI operations
func (ai *AILibrary) GetLibrary() *object.Library {
	functions := map[string]*object.Builtin{
		"completion": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: completion(model, messages)
				var model string

				// Handle positional arguments: completion(model, messages)
				if len(args) > 0 {
					// First arg should be model (string)
					if m, ok := args[0].(*object.String); ok {
						model = m.Value
					}
				}

				// Build messages from second positional argument
				var messages []Message
				if len(args) > 1 {
					if listObj, ok := args[1].(*object.List); ok {
						for _, el := range listObj.Elements {
							if dict, ok := el.(*object.Dict); ok {
								// extract role and content
								role := "user"
								content := ""
								if p, ok := dict.Pairs["role"]; ok {
									if s, ok := p.Value.(*object.String); ok {
										role = s.Value
									}
								}
								if p, ok := dict.Pairs["content"]; ok {
									if s, ok := p.Value.(*object.String); ok {
										content = s.Value
									}
								}
								messages = append(messages, Message{Role: role, Content: content})
							}
						}
					}
				}

				// Fallback simple message
				if len(messages) == 0 {
					messages = []Message{{Role: "user", Content: "Hello, please respond to this request."}}
				}

				req := &ChatCompletionRequest{
					Model:    model,
					Messages: messages,
				}

				// Get completion with automatic tool calling
				resp, err := ai.CreateChatCompletionWithTools(ctx, req)
				if err != nil {
					return errors.NewError("AI completion failed: %v", err)
				}

				// Return the response as a string
				if len(resp.Choices) > 0 {
					msg := &resp.Choices[0].Message
					if content := msg.GetContentAsString(); content != "" {
						return &object.String{Value: content}
					}
				}

				return &object.String{Value: ""}
			},
		},
	}

	return object.NewLibrary(functions, map[string]object.Object{}, "AI library for LLM completion")
}

// convertScriptlingDict converts a scriptling Dict to a regular Go map
func convertScriptlingDict(scriptDict *object.Dict) map[string]interface{} {
	result := make(map[string]interface{})
	for key, pair := range scriptDict.Pairs {
		switch v := pair.Value.(type) {
		case *object.String:
			result[key] = v.Value
		case *object.Integer:
			result[key] = v.Value
		case *object.Float:
			result[key] = v.Value
		case *object.Boolean:
			result[key] = v.Value
		case *object.List:
			result[key] = convertScriptlingList(v)
		case *object.Dict:
			result[key] = convertScriptlingDict(v)
		default:
			result[key] = v.Inspect()
		}
	}
	return result
}

// convertScriptlingList converts a scriptling List to a regular Go slice
func convertScriptlingList(scriptList *object.List) []interface{} {
	result := make([]interface{}, len(scriptList.Elements))
	for i, element := range scriptList.Elements {
		switch e := element.(type) {
		case *object.String:
			result[i] = e.Value
		case *object.Integer:
			result[i] = e.Value
		case *object.Float:
			result[i] = e.Value
		case *object.Boolean:
			result[i] = e.Value
		case *object.List:
			result[i] = convertScriptlingList(e)
		case *object.Dict:
			result[i] = convertScriptlingDict(e)
		default:
			result[i] = e.Inspect()
		}
	}
	return result
}

// MaxToolCallIterations is the maximum number of tool call iterations allowed
// to prevent infinite loops
const MaxToolCallIterations = 20

// toolCallKey creates a unique key for a tool call to detect duplicates
func toolCallKey(name string, args map[string]any) string {
	// Simple key based on tool name and serialized arguments
	argsJSON, _ := json.Marshal(args)
	return name + ":" + string(argsJSON)
}

// CreateChatCompletionWithTools creates a chat completion with automatic tool calling
// following proper multi-turn tool processing pattern
func (ai *AILibrary) CreateChatCompletionWithTools(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	currentMessages := req.Messages

	// Track recent tool calls to detect loops
	recentToolCalls := make(map[string]int) // key -> count
	var lastToolCallKey string

	// Add tools if MCP server is available - only tool_search and execute_tool
	if ai.router.mcpServer != nil {
		tools := ai.router.mcpServer.server.ListTools()
		req.Tools = openai.MCPToolsToOpenAIFiltered(tools, func(name string) bool {
			return name == "tool_search" || name == "execute_tool"
		})
	}

	// Multi-turn tool processing loop
	for iteration := 0; iteration < MaxToolCallIterations; iteration++ {
		req.Messages = currentMessages

		response, err := ai.router.CreateChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}

		// If no MCP server, no tool calls, or no choices, we're done
		if ai.router.mcpServer == nil || len(response.Choices) == 0 || len(response.Choices[0].Message.ToolCalls) == 0 {
			return response, nil
		}

		// Process tool calls - only process valid tool names
		message := response.Choices[0].Message
		var validToolCalls []openai.ToolCall
		for _, tc := range message.ToolCalls {
			// Skip malformed tool names (model confusion)
			if tc.Function.Name != "tool_search" && tc.Function.Name != "execute_tool" {
				continue
			}
			validToolCalls = append(validToolCalls, tc)
		}

		// If no valid tool calls after filtering, return the response
		if len(validToolCalls) == 0 {
			return response, nil
		}

		// Only process the first valid tool call to prevent batched confusion
		tc := validToolCalls[0]
		singleToolCall := []openai.ToolCall{tc}

		// Check for repeated identical tool calls (loop detection)
		key := toolCallKey(tc.Function.Name, tc.Function.Arguments)
		recentToolCalls[key]++

		// If we've seen this exact call 3+ times, the model is looping - force a response
		if recentToolCalls[key] >= 3 || (key == lastToolCallKey && recentToolCalls[key] >= 2) {
			// Remove tools and ask for final answer
			req.Messages = append(currentMessages, openai.BuildSystemMessage(
				"The tool has been called multiple times with the same result. Please provide your final answer based on the information gathered.",
			))
			req.Tools = nil
			return ai.router.CreateChatCompletion(ctx, req)
		}
		lastToolCallKey = key

		// Add assistant message with the single tool call
		currentMessages = append(currentMessages, openai.BuildAssistantToolCallMessage(
			message.GetContentAsString(),
			singleToolCall,
		))

		// Execute the single tool call
		toolResults, err := openai.ExecuteToolCalls(singleToolCall, func(name string, args map[string]any) (string, error) {
			response, err := ai.router.mcpServer.server.CallTool(ctx, name, args)
			if err != nil {
				return "", err
			}
			result, _ := openai.ExtractToolResult(response)
			return result, nil
		}, false)
		if err != nil {
			return nil, err
		}

		// Add tool results to conversation
		currentMessages = append(currentMessages, toolResults...)
	}

	return nil, openai.NewMaxToolIterationsError(MaxToolCallIterations)
}
