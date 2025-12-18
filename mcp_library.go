package main

import (
	"context"
	"encoding/json"
	"fmt"

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

// objectToGo converts a scriptling object to a Go value for JSON marshaling
func objectToGo(obj object.Object) interface{} {
	switch v := obj.(type) {
	case *object.String:
		return v.Value
	case *object.Integer:
		return v.Value
	case *object.Float:
		return v.Value
	case *object.Boolean:
		return v.Value
	case *object.Null:
		return nil
	case *object.List:
		result := make([]interface{}, len(v.Elements))
		for i, elem := range v.Elements {
			result[i] = objectToGo(elem)
		}
		return result
	case *object.Dict:
		result := make(map[string]interface{})
		for _, pair := range v.Pairs {
			if key, ok := pair.Key.(*object.String); ok {
				result[key.Value] = objectToGo(pair.Value)
			}
		}
		return result
	default:
		return fmt.Sprintf("%v", obj)
	}
}

// objectToGoMap converts a scriptling Dict to a Go map
func objectToGoMap(dict *object.Dict) map[string]interface{} {
	result := make(map[string]interface{})
	for key, pair := range dict.Pairs {
		result[key] = objectToGo(pair.Value)
	}
	return result
}

// getStringValue safely extracts a string value from a map
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// getFloatValue safely extracts a float64 value from a map
func getFloatValue(m map[string]interface{}, key string) float64 {
	if val, ok := m[key]; ok {
		if flt, ok := val.(float64); ok {
			return flt
		}
	}
	return 0.0
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
				goValue := objectToGo(args[0])
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
						toolArgs = objectToGoMap(argsObj)
					}
				}

				if toolName == "" {
					return &object.String{Value: "Error: tool name is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.String{Value: "Error: MCP server not available"}
				}

				// Call the tool directly via MCP server
				resp, err := m.mcpServer.server.CallTool(ctx, toolName, toolArgs)
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error: %v", err)}
				}

				if len(resp.Content) > 0 {
					return &object.String{Value: resp.Content[0].Text}
				}

				return &object.String{Value: ""}
			},
		},
		"search_tools": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				var query string = ""
				var namespace string = ""

				// Handle positional arguments: search_tools(query, namespace?)
				if len(args) > 0 {
					if q, ok := args[0].(*object.String); ok {
						query = q.Value
					}
				}
				if len(args) > 1 {
					if ns, ok := args[1].(*object.String); ok {
						namespace = ns.Value
					}
				}

				// Handle keyword arguments
				if q, ok := kwargs["query"]; ok {
					if qStr, ok := q.(*object.String); ok {
						query = qStr.Value
					}
				}
				if ns, ok := kwargs["namespace"]; ok {
					if nsStr, ok := ns.(*object.String); ok {
						namespace = nsStr.Value
					}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.List{Elements: []object.Object{}}
				}

				// Determine the MCP tool name to call
				toolName := "tool_search"
				if namespace != "" {
					// Ensure exactly one slash between namespace and tool name
					if namespace[len(namespace)-1] != '/' {
						toolName = namespace + "/" + toolName
					} else {
						toolName = namespace + toolName
					}
				}

				// Call the MCP tool_search tool
				searchArgs := map[string]interface{}{
					"query": query,
				}

				resp, err := m.mcpServer.server.CallTool(ctx, toolName, searchArgs)
				if err != nil {
					return &object.List{Elements: []object.Object{}}
				}

				// Parse the response (assuming it returns a list of tools)
				if len(resp.Content) > 0 {
					// Try to parse as JSON list of tools
					var tools []map[string]interface{}
					if err := json.Unmarshal([]byte(resp.Content[0].Text), &tools); err != nil {
						return &object.List{Elements: []object.Object{}}
					}

					result := &object.List{Elements: make([]object.Object, len(tools))}
					for i, tool := range tools {
						toolDict := &object.Dict{
							Pairs: map[string]object.DictPair{
								"name":        {Key: &object.String{Value: "name"}, Value: &object.String{Value: getStringValue(tool, "name")}},
								"description": {Key: &object.String{Value: "description"}, Value: &object.String{Value: getStringValue(tool, "description")}},
								"score":       {Key: &object.String{Value: "score"}, Value: &object.Float{Value: getFloatValue(tool, "score")}},
							},
						}
						result.Elements[i] = toolDict
					}
					return result
				}

				return &object.List{Elements: []object.Object{}}
			},
		},
		"execute_tool": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				var toolName string
				var toolArgs map[string]interface{}
				var namespace string

				// Handle positional arguments: execute_tool(name, args, namespace?)
				if len(args) >= 1 {
					if name, ok := args[0].(*object.String); ok {
						toolName = name.Value
					}
				}

				if len(args) >= 2 {
					if argsObj, ok := args[1].(*object.Dict); ok {
						toolArgs = objectToGoMap(argsObj)
					}
				}

				if len(args) >= 3 {
					if ns, ok := args[2].(*object.String); ok {
						namespace = ns.Value
					}
				}

				// Handle keyword arguments
				if name, ok := kwargs["name"]; ok {
					if nameStr, ok := name.(*object.String); ok {
						toolName = nameStr.Value
					}
				}
				if args, ok := kwargs["arguments"]; ok {
					if argsObj, ok := args.(*object.Dict); ok {
						toolArgs = objectToGoMap(argsObj)
					}
				}
				if ns, ok := kwargs["namespace"]; ok {
					if nsStr, ok := ns.(*object.String); ok {
						namespace = nsStr.Value
					}
				}

				if toolName == "" {
					return &object.String{Value: "Error: tool name is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.String{Value: "Error: MCP server not available"}
				}

				// Determine the MCP tool name to call
				mcpToolName := "execute_tool"
				if namespace != "" {
					// Ensure exactly one slash between namespace and tool name
					if namespace[len(namespace)-1] != '/' {
						mcpToolName = namespace + "/" + mcpToolName
					} else {
						mcpToolName = namespace + mcpToolName
					}
				}

				// Use the execute_tool MCP tool (which uses discovery)
				executeArgs := map[string]interface{}{
					"name":      toolName,
					"arguments": toolArgs,
				}

				resp, err := m.mcpServer.server.CallTool(ctx, mcpToolName, executeArgs)
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error: %v", err)}
				}

				if len(resp.Content) > 0 {
					return &object.String{Value: resp.Content[0].Text}
				}

				return &object.String{Value: ""}
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
					return &object.String{Value: "Error: code is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.String{Value: "Error: MCP server not available"}
				}

				// Use the execute_code MCP tool
				resp, err := m.mcpServer.server.CallTool(ctx, "execute_code", map[string]interface{}{
					"code": code,
				})
				if err != nil {
					return &object.String{Value: fmt.Sprintf("Error: %v", err)}
				}

				if len(resp.Content) > 0 {
					return &object.String{Value: resp.Content[0].Text}
				}

				return &object.String{Value: ""}
			},
		},
	}

	return object.NewLibrary(functions, map[string]object.Object{}, "MCP library for tool interaction")
}
