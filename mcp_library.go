package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp/toon"
	scriptlib "github.com/paularlott/scriptling"
	scriptlingmcp "github.com/paularlott/scriptling/mcp"
	"github.com/paularlott/scriptling/object"
)

// MCPLibrary provides MCP-related functions for Scriptling
type MCPLibrary struct {
	mcpServer *MCPServer
	result    *string                // Pointer to store return result
	args      map[string]interface{} // Arguments passed to the tool
}

// NewMCPLibrary creates a new MCP library instance
func NewMCPLibrary(mcpServer *MCPServer) *MCPLibrary {
	return &MCPLibrary{
		mcpServer: mcpServer,
		result:    nil,
		args:      make(map[string]interface{}),
	}
}

// SetArgs sets the arguments for this tool execution
func (m *MCPLibrary) SetArgs(args map[string]interface{}) {
	m.args = args
}

// SetResult sets the result that will be returned from the script
func (m *MCPLibrary) SetResult(result string) {
	m.result = &result
}

// GetResult returns the result set by the script, or nil if none set
func (m *MCPLibrary) GetResult() *string {
	return m.result
}

// GetLibrary returns the scriptling library object for MCP operations
func (m *MCPLibrary) GetLibrary() *object.Library {
	functions := map[string]*object.Builtin{
		"get": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: get requires parameter name"}
				}

				paramName, ok := args[0].(*object.String)
				if !ok {
					return &object.String{Value: "Error: parameter name must be a string"}
				}

				// Get the parameter from stored args
				if value, exists := m.args[paramName.Value]; exists {
					// Convert the value to appropriate scriptling object
					switch v := value.(type) {
					case string:
						return &object.String{Value: v}
					case int:
						return &object.Integer{Value: int64(v)}
					case int64:
						return &object.Integer{Value: v}
					case float64:
						return &object.Float{Value: v}
					case bool:
						return &object.Boolean{Value: v}
					default:
						return &object.String{Value: fmt.Sprintf("%v", v)}
					}
				}

				// If a default value was provided as second argument, return it
				if len(args) >= 2 {
					return args[1]
				}

				// No default value and parameter not found - return Null
				return &object.Null{}
			},
		},
		"return_string": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: return_string requires 1 argument"}
				}

				var result string
				switch v := args[0].(type) {
				case *object.String:
					result = v.Value
				default:
					result = fmt.Sprintf("%v", args[0])
				}

				m.SetResult(result)
				return &object.String{Value: result}
			},
		},
		"return_object": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: return_object requires 1 argument"}
				}

				// Convert the object to JSON for return
				var result string
				goValue := scriptlib.ToGo(args[0])
				jsonBytes, err := json.Marshal(goValue)
				if err != nil {
					result = fmt.Sprintf("%v", args[0])
				} else {
					result = string(jsonBytes)
				}
				m.SetResult(result)
				return &object.String{Value: result}
			},
		},
		"return_toon": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: return_toon requires 1 argument"}
				}

				// Convert the object to toon encoded string
				goValue := scriptlib.ToGo(args[0])
				encoded, err := toon.Encode(goValue)
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error encoding to toon: %v", err)}
				}
				m.SetResult(encoded)
				return &object.String{Value: encoded}
			},
		},
		"list_tools": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.List{Elements: []object.Object{}}
				}

				tools := m.mcpServer.server.ListTools()
				result := &object.List{Elements: make([]object.Object, len(tools))}

				for i, tool := range tools {
					toolDict := &object.Dict{
						Pairs: map[string]object.DictPair{
							"name":        {Key: &object.String{Value: "name"}, Value: &object.String{Value: tool.Name}},
							"description": {Key: &object.String{Value: "description"}, Value: &object.String{Value: tool.Description}},
						},
					}
					result.Elements[i] = toolDict
				}

				return result
			},
		},
		"call_tool": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				var toolName string
				var toolArgs map[string]interface{}

				// Handle positional arguments: call_tool(name, args)
				if len(args) >= 1 {
					if name, ok := args[0].(*object.String); ok {
						toolName = name.Value
					}
				}

				if len(args) >= 2 {
					if argsObj, ok := args[1].(*object.Dict); ok {
						if result := scriptlib.ToGo(argsObj); result != nil {
							toolArgs = result.(map[string]interface{})
						}
					}
				}

				if toolName == "" {
					return &object.Error{Message: "tool name is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.Error{Message: "MCP server not available"}
				}

				// Call the tool directly via MCP server
				resp, err := m.mcpServer.server.CallTool(ctx, toolName, toolArgs)
				if err != nil {
					return &object.Error{Message: fmt.Sprintf("tool call failed: %v", err)}
				}

				return scriptlingmcp.DecodeToolResponse(resp)
			},
		},
		"tool_search": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.Error{Message: "tool_search() requires a search query"}
				}

				// Get search query
				queryStr, ok := args[0].(*object.String)
				if !ok {
					return &object.Error{Message: "tool_search() first argument must be a string (search query)"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.Error{Message: "MCP server not available"}
				}

				// Call the tool_search tool directly
				searchArgs := map[string]interface{}{
					"query": queryStr.Value,
				}

				resp, err := m.mcpServer.server.CallTool(ctx, "tool_search", searchArgs)
				if err != nil {
					return &object.Error{Message: fmt.Sprintf("tool search failed: %v", err)}
				}

				result := scriptlingmcp.DecodeToolResponse(resp)

				// The response is a content block (possibly wrapped in a List), extract the tools from the text field
				var contentBlock *object.Dict
				if resultList, ok := result.(*object.List); ok && len(resultList.Elements) > 0 {
					if firstDict, ok := resultList.Elements[0].(*object.Dict); ok {
						contentBlock = firstDict
					}
				} else if resultDict, ok := result.(*object.Dict); ok {
					contentBlock = resultDict
				}

				if contentBlock != nil {
					// Try both "text" and "Text" keys (from JSON parsing)
					var textVal *object.String
					if val, found := contentBlock.Pairs["text"]; found {
						if s, ok := val.Value.(*object.String); ok {
							textVal = s
						}
					} else if val, found := contentBlock.Pairs["Text"]; found {
						if s, ok := val.Value.(*object.String); ok {
							textVal = s
						}
					}

					if textVal != nil {
						// Parse the JSON text to extract tools using the bridge package
						tools, err := scriptlingmcp.ParseToolSearchResultsFromText(textVal.Value)
						if err == nil {
							return tools
						}
					}
				}

				return result
			},
		},
		"execute_tool": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 2 {
					return &object.Error{Message: "execute_tool() requires tool name and arguments"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.Error{Message: "MCP server not available"}
				}

				// Get tool name (can be namespaced like "namespace/toolname")
				toolNameStr, ok := args[0].(*object.String)
				if !ok {
					return &object.Error{Message: "execute_tool() first argument must be a string (tool name)"}
				}

				// Get arguments
				argsDict, ok := args[1].(*object.Dict)
				if !ok {
					return &object.Error{Message: "execute_tool() second argument must be a dict (arguments)"}
				}

				// Convert arguments to map[string]interface{} for the execute_tool call
				arguments := make(map[string]interface{})
				for _, pair := range argsDict.Pairs {
					key := pair.Key.(*object.String).Value
					arguments[key] = scriptlib.ToGo(pair.Value)
				}

				// Call the execute_tool tool directly
				// Note: execute_tool expects arguments as a map/object, not as JSON string
				executeArgs := map[string]interface{}{
					"name":      toolNameStr.Value,
					"arguments": arguments,
				}

				resp, err := m.mcpServer.server.CallTool(ctx, "execute_tool", executeArgs)
				if err != nil {
					return &object.Error{Message: fmt.Sprintf("tool execution failed: %v", err)}
				}

				return scriptlingmcp.DecodeToolResponse(resp)
			},
		},
		"execute_code": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				var code string

				// Handle positional arguments: execute_code(code)
				if len(args) >= 1 {
					if c, ok := args[0].(*object.String); ok {
						code = c.Value
					}
				}

				// Handle keyword arguments
				if c, ok := kwargs["code"]; ok {
					if cStr, ok := c.(*object.String); ok {
						code = cStr.Value
					}
				}

				if code == "" {
					return &object.Error{Message: "code is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.Error{Message: "MCP server not available"}
				}

				// Use the execute_code MCP tool
				resp, err := m.mcpServer.server.CallTool(ctx, "execute_code", map[string]interface{}{
					"code": code,
				})
				if err != nil {
					return &object.Error{Message: fmt.Sprintf("code execution failed: %v", err)}
				}

				return scriptlingmcp.DecodeToolResponse(resp)
			},
		},
		"toon_encode": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: toon_encode requires 1 argument"}
				}

				goValue := scriptlib.ToGo(args[0])
				encoded, err := toon.Encode(goValue)
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error encoding to toon: %v", err)}
				}

				return &object.String{Value: encoded}
			},
		},
		"toon_decode": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				if len(args) < 1 {
					return &object.String{Value: "Error: toon_decode requires 1 argument"}
				}

				str, ok := args[0].(*object.String)
				if !ok {
					return &object.String{Value: "Error: toon_decode argument must be a string"}
				}

				decoded, err := toon.Decode(str.Value)
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error decoding from toon: %v", err)}
				}

				// Convert back to scriptling object
				switch v := decoded.(type) {
				case string:
					return &object.String{Value: v}
				case int:
					return &object.Integer{Value: int64(v)}
				case int64:
					return &object.Integer{Value: v}
				case float64:
					return &object.Float{Value: v}
				case bool:
					return &object.Boolean{Value: v}
				default:
					return &object.String{Value: fmt.Sprintf("%v", v)}
				}
			},
		},
	}

	return object.NewLibrary(functions, map[string]object.Object{}, "MCP library for tool interaction")
}
