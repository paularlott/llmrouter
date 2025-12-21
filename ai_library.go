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
	CreateEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
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
		"embedding": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: embedding(model, input)
				var model string
				var input interface{}

				if len(args) > 0 {
					if m, ok := args[0].(*object.String); ok {
						model = m.Value
					}
				}

				if len(args) > 1 {
					if s, ok := args[1].(*object.String); ok {
						input = s.Value
					} else if l, ok := args[1].(*object.List); ok {
						inputs := make([]string, len(l.Elements))
						for i, el := range l.Elements {
							if s, ok := el.(*object.String); ok {
								inputs[i] = s.Value
							}
						}
						input = inputs
					}
				}

				req := &EmbeddingRequest{
					Model: model,
					Input: input,
				}

				resp, err := ai.router.CreateEmbedding(ctx, req)
				if err != nil {
					return errors.NewError("Embedding failed: %v", err)
				}

				// Convert embeddings to scriptling list
				embeddings := make([]object.Object, len(resp.Data))
				for i, emb := range resp.Data {
					vector := make([]object.Object, len(emb.Embedding))
					for j, val := range emb.Embedding {
						vector[j] = &object.Float{Value: val}
					}
					embeddings[i] = &object.List{Elements: vector}
				}

				return &object.List{Elements: embeddings}
			},
		},
		"response_create": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: response_create(model, input, instructions=None, previous_response_id=None)
				if len(args) < 2 {
					return errors.NewError("response_create() requires at least 2 arguments (model, input)")
				}

				var model string
				var input []any
				var instructions string
				var previousResponseID string

				// Required positional arguments
				if m, ok := args[0].(*object.String); ok {
					model = m.Value
				} else {
					return errors.NewError("model must be a string")
				}

				// Input can be string or list
				if s, ok := args[1].(*object.String); ok {
					input = []any{s.Value}
				} else if l, ok := args[1].(*object.List); ok {
					for _, el := range l.Elements {
						if s, ok := el.(*object.String); ok {
							input = append(input, s.Value)
						}
					}
				} else {
					return errors.NewError("input must be a string or list of strings")
				}

				// Optional kwargs
				if instructionsObj, ok := kwargs["instructions"]; ok {
					if s, ok := instructionsObj.(*object.String); ok {
						instructions = s.Value
					}
				}

				if prevRespObj, ok := kwargs["previous_response_id"]; ok {
					if s, ok := prevRespObj.(*object.String); ok {
						previousResponseID = s.Value
					}
				}

				if ai.router.responsesService == nil {
					return errors.NewError("Responses service not available")
				}

				req := &CreateResponseRequest{
					Model:              model,
					Input:              input,
					Instructions:       instructions,
					PreviousResponseID: previousResponseID,
					Modalities:         []string{"text"}, // Default to text
				}

				resp, err := ai.router.responsesService.CreateResponse(ctx, req, ai.CreateChatCompletionWithTools) // Use AI library's tool-enabled completion
				if err != nil {
					return errors.NewError("Response creation failed: %v", err)
				}

				return &object.String{Value: resp.ID}
			},
		},
		"response_get": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: response_get(id)
				var id string

				if len(args) > 0 {
					if s, ok := args[0].(*object.String); ok {
						id = s.Value
					}
				}

				if ai.router.responsesService == nil {
					return errors.NewError("Responses service not available")
				}

				resp, err := ai.router.responsesService.GetResponse(ctx, id)
				if err != nil {
					return errors.NewError("Failed to get response: %v", err)
				}

				// Convert response to dict
				result := &object.Dict{Pairs: make(map[string]object.DictPair)}
				result.Pairs["id"] = object.DictPair{Key: &object.String{Value: "id"}, Value: &object.String{Value: resp.ID}}
				result.Pairs["status"] = object.DictPair{Key: &object.String{Value: "status"}, Value: &object.String{Value: resp.Status}}
				result.Pairs["model"] = object.DictPair{Key: &object.String{Value: "model"}, Value: &object.String{Value: resp.Model}}

				// Add output if available (new format: array of message objects)
				if len(resp.Output) > 0 {
					// Try to extract content from first message
					if msgMap, ok := resp.Output[0].(map[string]interface{}); ok {
						if contentArray, ok := msgMap["content"].([]interface{}); ok {
							// Extract text from content array
							var textParts []string
							for _, item := range contentArray {
								if contentItem, ok := item.(map[string]interface{}); ok {
									if text, ok := contentItem["text"].(string); ok {
										textParts = append(textParts, text)
									}
								}
							}
							if len(textParts) > 0 {
								result.Pairs["content"] = object.DictPair{
									Key:   &object.String{Value: "content"},
									Value: &object.String{Value: textParts[0]}, // Return first text part
								}
							}
						}
					}
				}

				// Add error if available
				if resp.Error != nil {
					result.Pairs["error"] = object.DictPair{
						Key:   &object.String{Value: "error"},
						Value: &object.String{Value: resp.Error.Message},
					}
				}

				return result
			},
		},
		"response_delete": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: response_delete(id)
				var id string

				if len(args) > 0 {
					if s, ok := args[0].(*object.String); ok {
						id = s.Value
					}
				}

				if ai.router.responsesService == nil {
					return errors.NewError("Responses service not available")
				}

				err := ai.router.responsesService.DeleteResponse(ctx, id)
				if err != nil {
					return errors.NewError("Failed to delete response: %v", err)
				}

				return &object.Boolean{Value: true}
			},
		},
		"response_cancel": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				// Parse arguments: response_cancel(id)
				var id string

				if len(args) > 0 {
					if s, ok := args[0].(*object.String); ok {
						id = s.Value
					}
				}

				if ai.router.responsesService == nil {
					return errors.NewError("Responses service not available")
				}

				resp, err := ai.router.responsesService.CancelResponse(ctx, id)
				if err != nil {
					return errors.NewError("Failed to cancel response: %v", err)
				}

				return &object.String{Value: resp.Status}
			},
		},
	}

	return object.NewLibrary(functions, map[string]object.Object{}, "AI library for LLM completion, embeddings, and responses")
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
