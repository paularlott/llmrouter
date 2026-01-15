package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp/toon"
	scriptlib "github.com/paularlott/scriptling"
	scriptlingmcp "github.com/paularlott/scriptling/extlibs/mcp"
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
	return object.NewLibraryBuilder("mcp", "MCP library for tool interaction").
		FunctionWithHelp("get", func(paramName string, defaultValue ...interface{}) interface{} {
			// Get the parameter from stored args
			if value, exists := m.args[paramName]; exists {
				return value
			}

			// Return default value if provided
			if len(defaultValue) > 0 {
				return defaultValue[0]
			}

			// No default value and parameter not found - return nil
			return nil
		}, "get(param_name, default=None) - Get a parameter value from tool arguments").
		FunctionWithHelp("return_string", func(value interface{}) string {
			result := fmt.Sprintf("%v", value)
			m.SetResult(result)
			return result
		}, "return_string(value) - Return a string result from the tool").
		FunctionWithHelp("return_object", func(value interface{}) (string, error) {
			// Convert the object to JSON for return
			jsonBytes, err := json.Marshal(value)
			if err != nil {
				result := fmt.Sprintf("%v", value)
				m.SetResult(result)
				return result, nil
			}
			result := string(jsonBytes)
			m.SetResult(result)
			return result, nil
		}, "return_object(value) - Return an object result from the tool as JSON").
		FunctionWithHelp("return_toon", func(value interface{}) (string, error) {
			// Convert the object to toon encoded string
			encoded, err := toon.Encode(value)
			if err != nil {
				return "", fmt.Errorf("error encoding to toon: %v", err)
			}
			m.SetResult(encoded)
			return encoded, nil
		}, "return_toon(value) - Return an object result from the tool as TOON encoded string").
		FunctionWithHelp("list_tools", func() []map[string]string {
			if m.mcpServer == nil || m.mcpServer.server == nil {
				return []map[string]string{}
			}

			tools := m.mcpServer.server.ListTools()
			result := make([]map[string]string, len(tools))

			for i, tool := range tools {
				result[i] = map[string]string{
					"name":        tool.Name,
					"description": tool.Description,
				}
			}

			return result
		}, "list_tools() - List all available MCP tools").
		FunctionWithHelp("call_tool", func(toolName string, toolArgs map[string]interface{}) (interface{}, error) {
			if m.mcpServer == nil || m.mcpServer.server == nil {
				return nil, fmt.Errorf("MCP server not available")
			}

			// Call the tool directly via MCP server
			resp, err := m.mcpServer.server.CallTool(context.Background(), toolName, toolArgs)
			if err != nil {
				return nil, fmt.Errorf("tool call failed: %v", err)
			}

			// Convert response to Go value
			result := scriptlingmcp.DecodeToolResponse(resp)
			return scriptlib.ToGo(result), nil
		}, "call_tool(name, args) - Call an MCP tool directly").
		FunctionWithHelp("tool_search", func(query string) (interface{}, error) {
			if m.mcpServer == nil || m.mcpServer.server == nil {
				return nil, fmt.Errorf("MCP server not available")
			}

			// Call the tool_search tool directly
			searchArgs := map[string]interface{}{
				"query": query,
			}

			resp, err := m.mcpServer.server.CallTool(context.Background(), "tool_search", searchArgs)
			if err != nil {
				return nil, fmt.Errorf("tool search failed: %v", err)
			}

			result := scriptlingmcp.DecodeToolResponse(resp)
			return scriptlib.ToGo(result), nil
		}, "tool_search(query) - Search for available tools by keyword").
		FunctionWithHelp("execute_tool", func(toolName string, arguments map[string]interface{}) (interface{}, error) {
			if m.mcpServer == nil || m.mcpServer.server == nil {
				return nil, fmt.Errorf("MCP server not available")
			}

			// Call the execute_tool tool directly
			executeArgs := map[string]interface{}{
				"name":      toolName,
				"arguments": arguments,
			}

			resp, err := m.mcpServer.server.CallTool(context.Background(), "execute_tool", executeArgs)
			if err != nil {
				return nil, fmt.Errorf("tool execution failed: %v", err)
			}

			result := scriptlingmcp.DecodeToolResponse(resp)
			return scriptlib.ToGo(result), nil
		}, "execute_tool(name, args) - Execute a discovered tool with arguments").
		FunctionWithHelp("execute_code", func(code string) (interface{}, error) {
			if m.mcpServer == nil || m.mcpServer.server == nil {
				return nil, fmt.Errorf("MCP server not available")
			}

			// Use the execute_code MCP tool
			resp, err := m.mcpServer.server.CallTool(context.Background(), "execute_code", map[string]interface{}{
				"code": code,
			})
			if err != nil {
				return nil, fmt.Errorf("code execution failed: %v", err)
			}

			result := scriptlingmcp.DecodeToolResponse(resp)
			return scriptlib.ToGo(result), nil
		}, "execute_code(code) - Execute arbitrary Python/Scriptling code").
		FunctionWithHelp("toon_encode", func(value interface{}) (string, error) {
			encoded, err := toon.Encode(value)
			if err != nil {
				return "", fmt.Errorf("error encoding to toon: %v", err)
			}
			return encoded, nil
		}, "toon_encode(value) - Encode an object to TOON format").
		FunctionWithHelp("toon_decode", func(encoded string) (interface{}, error) {
			decoded, err := toon.Decode(encoded)
			if err != nil {
				return nil, fmt.Errorf("error decoding from toon: %v", err)
			}
			return decoded, nil
		}, "toon_decode(encoded) - Decode a TOON formatted string to an object").
		Build()
}
