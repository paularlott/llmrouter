package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp"
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

// decodeToolResponse intelligently decodes a tool response for easier use in scripts
// - Single text content: returns the text as a string
// - Text that is valid JSON: returns the parsed JSON as objects
// - Multiple content blocks: returns the list of decoded blocks
// - Structured content: returns the decoded structure
// - Image/Resource blocks: returns the decoded block with Type, Data, etc.
func decodeToolResponse(response *mcp.ToolResponse) object.Object {
	// Check for structured content first
	if response.StructuredContent != nil {
		return convertToScriptlingObject(response.StructuredContent)
	}

	// Handle content blocks
	content := response.Content
	if len(content) == 0 {
		return &object.Null{}
	}

	// Single content block
	if len(content) == 1 {
		return decodeToolContent(content[0])
	}

	// Multiple content blocks - decode each
	elements := make([]object.Object, len(content))
	for i, block := range content {
		elements[i] = decodeToolContent(block)
	}
	return &object.List{Elements: elements}
}

// decodeToolContent decodes a single content block
func decodeToolContent(block mcp.ToolContent) object.Object {
	switch block.Type {
	case "text":
		if block.Text != "" {
			return decodeToolText(block.Text)
		}
		return &object.String{Value: ""}
	case "image":
		// Return image block with data and mimeType
		result := &object.Dict{Pairs: map[string]object.DictPair{
			"Type":     {Key: &object.String{Value: "Type"}, Value: &object.String{Value: "image"}},
			"Data":     {Key: &object.String{Value: "Data"}, Value: &object.String{Value: block.Data}},
			"MimeType": {Key: &object.String{Value: "MimeType"}, Value: &object.String{Value: block.MimeType}},
		}}
		return result
	case "resource":
		// Return resource block
		return convertToScriptlingObject(block.Resource)
	default:
		// Unknown type, return as dict
		result := &object.Dict{Pairs: map[string]object.DictPair{
			"Type": {Key: &object.String{Value: "Type"}, Value: &object.String{Value: block.Type}},
		}}
		if block.Text != "" {
			result.Pairs["Text"] = object.DictPair{Key: &object.String{Value: "Text"}, Value: &object.String{Value: block.Text}}
		}
		if block.Data != "" {
			result.Pairs["Data"] = object.DictPair{Key: &object.String{Value: "Data"}, Value: &object.String{Value: block.Data}}
		}
		return result
	}
}

// decodeToolText decodes text content, parsing JSON if valid
func decodeToolText(text string) object.Object {
	// Try to parse as JSON
	var jsonValue interface{}
	if err := json.Unmarshal([]byte(text), &jsonValue); err == nil {
		return convertToScriptlingObject(jsonValue)
	}
	// Return as plain string
	return &object.String{Value: text}
}

// convertToScriptlingObject converts a Go interface{} to a scriptling object
func convertToScriptlingObject(v interface{}) object.Object {
	switch val := v.(type) {
	case nil:
		return &object.Null{}
	case bool:
		return &object.Boolean{Value: val}
	case float64:
		if val == float64(int64(val)) {
			return object.NewInteger(int64(val))
		}
		return &object.Float{Value: val}
	case int:
		return object.NewInteger(int64(val))
	case int64:
		return object.NewInteger(val)
	case string:
		return &object.String{Value: val}
	case []interface{}:
		elements := make([]object.Object, len(val))
		for i, elem := range val {
			elements[i] = convertToScriptlingObject(elem)
		}
		return &object.List{Elements: elements}
	case map[string]interface{}:
		pairs := make(map[string]object.DictPair)
		for k, v := range val {
			key := &object.String{Value: k}
			value := convertToScriptlingObject(v)
			pairs[k] = object.DictPair{Key: key, Value: value}
		}
		return &object.Dict{Pairs: pairs}
	default:
		return &object.String{Value: fmt.Sprintf("%v", val)}
	}
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

				return decodeToolResponse(resp)
			},
		},
		"tool_search": {
			Fn: func(ctx context.Context, kwargs map[string]object.Object, args ...object.Object) object.Object {
				var query string = ""
				var namespace string = ""

				// Handle positional arguments: tool_search(query, namespace?)
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
					return &object.Error{Message: "MCP server not available"}
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
					return &object.Error{Message: fmt.Sprintf("tool search failed: %v", err)}
				}

				// Decode the response - should return a list of tools
				decoded := decodeToolResponse(resp)

				// If already a list, return it
				if resultList, ok := decoded.(*object.List); ok {
					return resultList
				}

				// If it's a dict with results field, extract it
				if resultDict, ok := decoded.(*object.Dict); ok {
					if resultsVal, found := resultDict.Pairs["results"]; found {
						if resultsList, ok := resultsVal.Value.(*object.List); ok {
							return resultsList
						}
					}
				}

				// Fallback: return empty list
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
					return &object.Error{Message: "tool name is required"}
				}

				if m.mcpServer == nil || m.mcpServer.server == nil {
					return &object.Error{Message: "MCP server not available"}
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
					return &object.Error{Message: fmt.Sprintf("tool execution failed: %v", err)}
				}

				return decodeToolResponse(resp)
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

				return decodeToolResponse(resp)
			},
		},
	}

	return object.NewLibrary(functions, map[string]object.Object{}, "MCP library for tool interaction")
}
