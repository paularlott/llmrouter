package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/discovery"
	"github.com/paularlott/scriptling"
	"github.com/paularlott/scriptling/extlibs"
	"github.com/paularlott/scriptling/object"
	"github.com/paularlott/scriptling/stdlib"
)

// ScriptToolProvider implements discovery.ToolProvider for dynamic script tool discovery
// This allows tools to be added/removed/edited without restarting the server
type ScriptToolProvider struct {
	mcpServer *MCPServer
	mu        sync.RWMutex
}

// toolConfig holds parsed tool.toml configuration
type toolConfig struct {
	Name        string                   `toml:"name"`
	Description string                   `toml:"description"`
	Keywords    []string                 `toml:"keywords"`
	Script      string                   `toml:"script"`
	Parameters  map[string]toolParameter `toml:"parameters"`
}

// NewScriptToolProvider creates a new script tool provider
func NewScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider {
	return &ScriptToolProvider{
		mcpServer: mcpServer,
	}
}

// scanTools scans the tools directory and returns all valid tool configurations
func (p *ScriptToolProvider) scanTools() (map[string]*toolConfig, error) {
	tools := make(map[string]*toolConfig)

	if p.mcpServer.toolsPath == "" {
		return tools, nil
	}

	// Ensure tools directory exists
	if _, err := os.Stat(p.mcpServer.toolsPath); os.IsNotExist(err) {
		return tools, nil
	}

	// Walk through tools directory looking for tool.toml files
	err := filepath.Walk(p.mcpServer.toolsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue on error
		}

		if info.IsDir() || !strings.HasSuffix(info.Name(), "tool.toml") {
			return nil
		}

		toolDir := filepath.Dir(path)
		toolName := filepath.Base(toolDir)

		// Parse tool.toml
		var cfg toolConfig
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			p.mcpServer.logger.Warn("failed to parse tool.toml", "path", path, "error", err)
			return nil // Continue processing other tools
		}

		// Use directory name if name not specified
		if cfg.Name == "" {
			cfg.Name = toolName
		}

		// Validate required fields
		if cfg.Script == "" {
			p.mcpServer.logger.Warn("tool missing script field", "tool", cfg.Name)
			return nil
		}

		// Build script path
		scriptPath := filepath.Join(toolDir, cfg.Script)

		// Verify script exists
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			p.mcpServer.logger.Warn("tool script not found", "tool", cfg.Name, "script", scriptPath)
			return nil
		}

		tools[cfg.Name] = &cfg
		return nil
	})

	return tools, err
}

// ListToolMetadata returns metadata for all tools from the filesystem
func (p *ScriptToolProvider) ListToolMetadata(ctx context.Context) ([]discovery.ToolMetadata, error) {
	tools, err := p.scanTools()
	if err != nil {
		return nil, err
	}

	var metadata []discovery.ToolMetadata
	for _, cfg := range tools {
		metadata = append(metadata, discovery.ToolMetadata{
			Name:        cfg.Name,
			Description: cfg.Description,
			Keywords:    cfg.Keywords,
		})
	}

	return metadata, nil
}

// GetTool returns the full tool definition for a specific tool
func (p *ScriptToolProvider) GetTool(ctx context.Context, name string) (*mcp.MCPTool, error) {
	tools, err := p.scanTools()
	if err != nil {
		return nil, err
	}

	cfg, exists := tools[name]
	if !exists {
		return nil, nil // Tool not found
	}

	// Build parameters list
	var params []mcp.Parameter
	for paramName, param := range cfg.Parameters {
		switch param.Type {
		case "string":
			if param.Required {
				params = append(params, mcp.String(paramName, param.Description, mcp.Required()))
			} else {
				params = append(params, mcp.String(paramName, param.Description))
			}
		case "number":
			if param.Required {
				params = append(params, mcp.Number(paramName, param.Description, mcp.Required()))
			} else {
				params = append(params, mcp.Number(paramName, param.Description))
			}
		case "boolean":
			if param.Required {
				params = append(params, mcp.Boolean(paramName, param.Description, mcp.Required()))
			} else {
				params = append(params, mcp.Boolean(paramName, param.Description))
			}
		default:
			if param.Required {
				params = append(params, mcp.String(paramName, param.Description, mcp.Required()))
			} else {
				params = append(params, mcp.String(paramName, param.Description))
			}
		}
	}

	// Build MCP tool with input schema
	toolBuilder := mcp.NewTool(cfg.Name, cfg.Description, params...)

	// Build the schema
	schema := toolBuilder.BuildSchema()
	return &mcp.MCPTool{
		Name:        cfg.Name,
		Description: cfg.Description,
		InputSchema: schema,
	}, nil
}

// CallTool executes a tool by name
func (p *ScriptToolProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error) {
	tools, err := p.scanTools()
	if err != nil {
		return nil, err
	}

	cfg, exists := tools[name]
	if !exists {
		return nil, discovery.ErrToolNotFound
	}

	// Find the script path
	var scriptPath string
	err = filepath.Walk(p.mcpServer.toolsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), "tool.toml") {
			return nil
		}

		toolDir := filepath.Dir(path)
		toolName := filepath.Base(toolDir)

		// Check if this is our tool
		var testCfg struct {
			Name string `toml:"name"`
		}
		if _, err := toml.DecodeFile(path, &testCfg); err != nil {
			return nil
		}
		if testCfg.Name == "" {
			testCfg.Name = toolName
		}
		if testCfg.Name == name {
			scriptPath = filepath.Join(toolDir, cfg.Script)
			return filepath.SkipAll
		}
		return nil
	})

	if scriptPath == "" {
		return nil, discovery.ErrToolNotFound
	}

	return p.mcpServer.executeScriptToolFromPath(scriptPath, mcp.NewToolRequest(args))
}

// Ensure ScriptToolProvider implements ToolProvider
var _ discovery.ToolProvider = (*ScriptToolProvider)(nil)

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server        *mcp.Server
	registry      *discovery.ToolRegistry
	scriptling    *scriptling.Scriptling
	config        *Config
	logger        Logger
	router        *Router
	toolsPath     string
	librariesPath string
}

// setupScriptlingEnvironment configures a Scriptling environment with all standard libraries
func setupScriptlingEnvironment(env *scriptling.Scriptling) {
	// Register core libraries
	stdlib.RegisterAll(env)
	extlibs.RegisterRequestsLibrary(env)
	extlibs.RegisterSysLibrary(env, []string{})
	extlibs.RegisterSecretsLibrary(env)
	extlibs.RegisterSubprocessLibrary(env)
	extlibs.RegisterHTMLParserLibrary(env)
	extlibs.RegisterThreadsLibrary(env)
	extlibs.RegisterOSLibrary(env, []string{})
	extlibs.RegisterPathlibLibrary(env, []string{})

	// Enable output capture
	env.EnableOutputCapture()
}

// setupScriptlingEnvironmentWithAI configures a Scriptling environment with all standard libraries plus AI and MCP libraries
func setupScriptlingEnvironmentWithAI(env *scriptling.Scriptling, router *Router, mcpServer *MCPServer) {
	// Setup standard environment
	setupScriptlingEnvironment(env)

	// Create and register AI library
	aiLib := NewAILibrary(router)
	env.RegisterLibrary("ai", aiLib.GetLibrary())

	// Create and register MCP library if mcpServer is provided
	if mcpServer != nil {
		mcpLib := NewMCPLibrary(mcpServer)
		env.RegisterLibrary("mcp", mcpLib.GetLibrary())
	}
}

// setupScriptlingEnvironmentWithAIAndResult configures a Scriptling environment with result tracking
func setupScriptlingEnvironmentWithAIAndResult(env *scriptling.Scriptling, router *Router, mcpServer *MCPServer, mcpLib *MCPLibrary) {
	// Setup standard environment
	setupScriptlingEnvironment(env)

	// Create and register AI library
	aiLib := NewAILibrary(router)
	env.RegisterLibrary("ai", aiLib.GetLibrary())

	// Register the provided MCP library instance
	if mcpLib != nil {
		env.RegisterLibrary("mcp", mcpLib.GetLibrary())
	}
}

// setupOnDemandLibraryLoading configures dynamic library loading for a Scriptling instance
func (m *MCPServer) setupOnDemandLibraryLoading(scriptlingInstance *scriptling.Scriptling) {
	scriptlingInstance.SetOnDemandLibraryCallback(func(p *scriptling.Scriptling, libName string) bool {
		if m.librariesPath == "" {
			return false
		}

		filename := filepath.Join(m.librariesPath, libName+".py")
		content, err := os.ReadFile(filename)
		if err != nil {
			// Try in the current directory as fallback
			filename = libName + ".py"
			content, err = os.ReadFile(filename)
			if err != nil {
				return false
			}
		}

		if err := p.RegisterScriptLibrary(libName, string(content)); err != nil {
			m.logger.Warn("failed to register dynamic library", "library", libName, "error", err)
			return false
		}

		m.logger.Debug("loaded dynamic library", "library", libName, "path", filename)
		return true
	})
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(config *Config, logger Logger, router *Router) (*MCPServer, error) {
	// Create MCP server
	server := mcp.NewServer("llmrouter", "1.0.0")
	server.SetInstructions(`This server provides AI completion with tool calling support and Scriptling execution capabilities.
Use tool_search to discover available tools.
Use execute_tool to run discovered tools.
Use execute_code for custom Scriptling/Python code execution.`)

	// Create discovery registry
	registry := discovery.NewToolRegistry()

	mcpServer := &MCPServer{
		server:        server,
		registry:      registry,
		config:        config,
		logger:        logger,
		router:        router,
		toolsPath:     config.Scriptling.ToolsPath,
		librariesPath: config.Scriptling.LibrariesPath,
	}

	// Initialize Scriptling environment
	if err := mcpServer.initializeScriptling(); err != nil {
		return nil, fmt.Errorf("failed to initialize scriptling: %w", err)
	}

	// Register tools
	if err := mcpServer.registerTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// Connect to remote MCP servers
	for _, remoteServer := range config.MCP.RemoteServers {
		if remoteServer.Token != "" {
			// For now, skip token auth as API might have changed
			logger.Warn("remote MCP server token auth not implemented yet", "namespace", remoteServer.Namespace, "url", remoteServer.URL)
		}
		// Try to connect without auth for now
		if err := server.RegisterRemoteServer(remoteServer.URL, remoteServer.Namespace, nil); err != nil {
			logger.Warn("failed to connect to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "error", err)
		} else {
			logger.Info("connected to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL)
		}
	}

	// Attach registry to server (this registers tool_search and execute_tool)
	registry.Attach(server)

	return mcpServer, nil
}

// initializeScriptling sets up the Scriptling environment
func (m *MCPServer) initializeScriptling() error {
	m.scriptling = scriptling.New()

	// Setup the Scriptling environment with AI and MCP libraries
	setupScriptlingEnvironmentWithAI(m.scriptling, m.router, m)

	// Setup on-demand library loading
	m.setupOnDemandLibraryLoading(m.scriptling)

	return nil
}

// registerTools registers all available tools
func (m *MCPServer) registerTools() error {
	// Register execute_code tool for running arbitrary scripts
	m.server.RegisterTool(
		mcp.NewTool("execute_code", "Execute arbitrary Python/Scriptling code. Use this to run custom scripts.",
			mcp.String("code", "The Python/Scriptling code to execute", mcp.Required()),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			code, ok := req.Args()["code"].(string)
			if !ok {
				return nil, fmt.Errorf("code parameter is required and must be a string")
			}
			return m.executeScriptTool(code, req)
		},
	)

	// Add dynamic script tool provider
	// This allows tools to be added/removed/edited without restarting the server
	scriptProvider := NewScriptToolProvider(m)
	m.registry.AddProvider(scriptProvider)
	m.logger.Info("registered dynamic script tool provider", "tools_path", m.toolsPath)

	return nil
}

// toolParameter defines a tool parameter from tool.toml
type toolParameter struct {
	Type        string `toml:"type"`
	Description string `toml:"description"`
	Required    bool   `toml:"required"`
}

// executeScriptToolFromPath reads the script from disk and executes it
// This allows scripts to be edited without restarting the server
func (m *MCPServer) executeScriptToolFromPath(scriptPath string, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script file %s: %w", scriptPath, err)
	}
	return m.executeScriptTool(string(content), req)
}

// executeScriptTool executes a tool script with arguments
func (m *MCPServer) executeScriptTool(scriptContent string, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	// Create a fresh environment for this execution
	env := scriptling.New()

	// Create MCP library instance to track results
	mcpLib := NewMCPLibrary(m)

	// Setup the Scriptling environment with AI and MCP libraries
	setupScriptlingEnvironmentWithAIAndResult(env, m.router, m, mcpLib)

	// Copy on-demand library callback to this environment
	// Note: SetOnDemandLibraryCallback is on the Scriptling instance, not environment
	m.setupOnDemandLibraryLoading(env)

	// For now, we'll pass arguments in a simpler way
	// by prepending them to the script content
	args := make(map[string]interface{})
	for key, value := range req.Args() {
		args[key] = value
	}

	// Set the arguments on the MCP library
	mcpLib.SetArgs(args)

	// Prepend arguments as Python variables at the top of the script
	if len(args) > 0 {
		var argsPrepended strings.Builder
		for k, v := range args {
			// Convert value to appropriate Python literal
			switch val := v.(type) {
			case string:
				// Use Python-style string escaping with triple quotes to handle special characters
				escaped := strings.ReplaceAll(val, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				escaped = strings.ReplaceAll(escaped, "\n", "\\n")
				escaped = strings.ReplaceAll(escaped, "\r", "\\r")
				escaped = strings.ReplaceAll(escaped, "\t", "\\t")
				argsPrepended.WriteString(fmt.Sprintf("%s = \"%s\"\n", k, escaped))
			case int, int64:
				argsPrepended.WriteString(fmt.Sprintf("%s = %d\n", k, val))
			case float64:
				argsPrepended.WriteString(fmt.Sprintf("%s = %f\n", k, val))
			case bool:
				argsPrepended.WriteString(fmt.Sprintf("%s = %t\n", k, val))
			default:
				// Convert to string and escape
				strVal := fmt.Sprintf("%v", val)
				escaped := strings.ReplaceAll(strVal, "\\", "\\\\")
				escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
				escaped = strings.ReplaceAll(escaped, "\n", "\\n")
				escaped = strings.ReplaceAll(escaped, "\r", "\\r")
				escaped = strings.ReplaceAll(escaped, "\t", "\\t")
				argsPrepended.WriteString(fmt.Sprintf("%s = \"%s\"\n", k, escaped))
			}
		}
		argsPrepended.WriteString("\n")
		scriptContent = argsPrepended.String() + scriptContent
	}

	// Execute the script
	result, err := env.Eval(scriptContent)
	output := env.GetOutput()

	// Check if MCP library set a result
	if mcpResult := mcpLib.GetResult(); mcpResult != nil {
		return mcp.NewToolResponseText(*mcpResult), nil
	}

	var response strings.Builder
	if output != "" {
		response.WriteString(output)
	}
	if err != nil {
		response.WriteString(fmt.Sprintf("\nError: %s", err.Error()))
	} else if result != nil && result.Type() != object.NULL_OBJ {
		if response.Len() > 0 {
			response.WriteString("\n")
		}
		response.WriteString(fmt.Sprintf("Result: %s", result.Inspect()))
	}

	return mcp.NewToolResponseText(response.String()), nil
}

// HandleRequest handles HTTP requests to the MCP server
func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	m.server.HandleRequest(w, r)
}
