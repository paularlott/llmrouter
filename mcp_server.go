package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/paularlott/llmrouter/log"
	"github.com/paularlott/mcp"
	"github.com/paularlott/scriptling"
	"github.com/paularlott/scriptling/extlibs"
	scriptlingai "github.com/paularlott/scriptling/extlibs/ai"
	scriptlingmcp "github.com/paularlott/scriptling/extlibs/mcp"
	"github.com/paularlott/scriptling/object"
	"github.com/paularlott/scriptling/stdlib"
)

// ScriptToolProvider implements mcp.ToolProvider for dynamic script tools
// It filters tools based on the visibility parameter (native or ondemand)
type ScriptToolProvider struct {
	mcpServer  *MCPServer
	visibility string // "native" or "ondemand"
}

// toolConfig holds parsed tool.toml configuration
type toolConfig struct {
	Name        string                   `toml:"name"`
	Description string                   `toml:"description"`
	Keywords    []string                 `toml:"keywords"`
	Script      string                   `toml:"script"`
	Visibility  string                   `toml:"visibility"` // "native" (default) or "ondemand"
	Parameters  map[string]toolParameter `toml:"parameters"`
}

// toolParameter defines a tool parameter from tool.toml
type toolParameter struct {
	Type        string `toml:"type"`
	Description string `toml:"description"`
	Required    bool   `toml:"required"`
}

// NewNativeScriptToolProvider creates a provider that returns only native-visibility tools
func NewNativeScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider {
	return &ScriptToolProvider{
		mcpServer:  mcpServer,
		visibility: "native",
	}
}

// NewOnDemandScriptToolProvider creates a provider that returns only ondemand-visibility tools
func NewOnDemandScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider {
	return &ScriptToolProvider{
		mcpServer:  mcpServer,
		visibility: "ondemand",
	}
}

// NewScriptToolProvider creates a provider for native tools (backwards compatible)
func NewScriptToolProvider(mcpServer *MCPServer) *ScriptToolProvider {
	return NewNativeScriptToolProvider(mcpServer)
}

// scanTools scans the tools directory and returns all valid tool configurations
func (p *ScriptToolProvider) scanTools() (map[string]*toolConfig, error) {
	tools := make(map[string]*toolConfig)

	if p.mcpServer.toolsPath == "" {
		return tools, nil
	}

	if _, err := os.Stat(p.mcpServer.toolsPath); os.IsNotExist(err) {
		return tools, nil
	}

	err := filepath.Walk(p.mcpServer.toolsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(info.Name(), "tool.toml") {
			return nil
		}

		toolDir := filepath.Dir(path)
		toolName := filepath.Base(toolDir)

		var cfg toolConfig
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			p.mcpServer.logger.Warn("failed to parse tool.toml", "path", path, "error", err)
			return nil
		}

		if cfg.Name == "" {
			cfg.Name = toolName
		}

		if cfg.Script == "" {
			p.mcpServer.logger.Warn("tool missing script field", "tool", cfg.Name)
			return nil
		}

		scriptPath := filepath.Join(toolDir, cfg.Script)
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			p.mcpServer.logger.Warn("tool script not found", "tool", cfg.Name, "script", scriptPath)
			return nil
		}

		tools[cfg.Name] = &cfg
		return nil
	})

	return tools, err
}

// GetTools returns script tools filtered by the provider's visibility setting.
// Native provider returns tools with visibility="native" or no visibility set (default).
// OnDemand provider returns tools with visibility="ondemand".
func (p *ScriptToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
	tools, err := p.scanTools()
	if err != nil {
		return nil, err
	}

	var mcpTools []mcp.MCPTool
	for _, cfg := range tools {
		// Filter based on provider's visibility setting
		toolVisibility := cfg.Visibility
		if toolVisibility == "" {
			toolVisibility = "native" // Default to native
		}
		if toolVisibility != p.visibility {
			continue // Skip tools that don't match our visibility filter
		}

		params := buildParameters(cfg.Parameters)
		toolBuilder := mcp.NewTool(cfg.Name, cfg.Description, params...)
		schema := toolBuilder.BuildSchema()

		mcpTools = append(mcpTools, mcp.MCPTool{
			Name:        cfg.Name,
			Description: cfg.Description,
			InputSchema: schema,
			Keywords:    cfg.Keywords,
		})
	}

	return mcpTools, nil
}

// ExecuteTool executes a tool by name (handles both native and ondemand tools)
func (p *ScriptToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	tools, err := p.scanTools()
	if err != nil {
		return nil, err
	}

	cfg, exists := tools[name]
	if !exists {
		return nil, mcp.ErrUnknownTool
	}

	// Check if this tool matches our visibility filter
	toolVisibility := cfg.Visibility
	if toolVisibility == "" {
		toolVisibility = "native"
	}
	if toolVisibility != p.visibility {
		return nil, mcp.ErrUnknownTool // Not handled by this provider
	}

	// Find script path
	var scriptPath string
	filepath.Walk(p.mcpServer.toolsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), "tool.toml") {
			return nil
		}

		toolDir := filepath.Dir(path)
		toolName := filepath.Base(toolDir)

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
		return nil, mcp.ErrUnknownTool
	}

	response, err := p.mcpServer.executeScriptToolFromPath(scriptPath, mcp.NewToolRequest(params))
	if err != nil {
		return nil, err
	}
	return response.Content, nil
}

var _ mcp.ToolProvider = (*ScriptToolProvider)(nil)

// MCPServer wraps the MCP server functionality
type MCPServer struct {
	server        *mcp.Server
	scriptling    *scriptling.Scriptling
	config        *Config
	logger        Logger
	router        *Router
	toolsPath     string
	librariesPath string
}

// buildParameters converts tool parameters to mcp.Parameter slice
func buildParameters(params map[string]toolParameter) []mcp.Parameter {
	var result []mcp.Parameter
	for paramName, param := range params {
		switch param.Type {
		case "string":
			if param.Required {
				result = append(result, mcp.String(paramName, param.Description, mcp.Required()))
			} else {
				result = append(result, mcp.String(paramName, param.Description))
			}
		case "number":
			if param.Required {
				result = append(result, mcp.Number(paramName, param.Description, mcp.Required()))
			} else {
				result = append(result, mcp.Number(paramName, param.Description))
			}
		case "boolean":
			if param.Required {
				result = append(result, mcp.Boolean(paramName, param.Description, mcp.Required()))
			} else {
				result = append(result, mcp.Boolean(paramName, param.Description))
			}
		default:
			if param.Required {
				result = append(result, mcp.String(paramName, param.Description, mcp.Required()))
			} else {
				result = append(result, mcp.String(paramName, param.Description))
			}
		}
	}
	return result
}

// setupScriptlingEnvironment configures a Scriptling environment with all standard libraries
func setupScriptlingEnvironment(env *scriptling.Scriptling) {
	stdlib.RegisterAll(env)
	extlibs.RegisterRequestsLibrary(env)
	extlibs.RegisterSysLibrary(env, []string{})
	extlibs.RegisterSecretsLibrary(env)
	extlibs.RegisterSubprocessLibrary(env)
	extlibs.RegisterHTMLParserLibrary(env)
	extlibs.RegisterThreadsLibrary(env)
	extlibs.RegisterOSLibrary(env, []string{})
	extlibs.RegisterPathlibLibrary(env, []string{})
	extlibs.RegisterWaitForLibrary(env)
	extlibs.RegisterGlobLibrary(env, []string{})
	scriptlingai.Register(env)
	scriptlingmcp.Register(env)
	scriptlingmcp.RegisterToon(env)
	env.EnableOutputCapture()
}

// setupScriptlingEnvironmentWithAI configures a Scriptling environment with AI and MCP libraries
func setupScriptlingEnvironmentWithAI(env *scriptling.Scriptling, router *Router, mcpServer *MCPServer) {
	setupScriptlingEnvironment(env)
	aiLib := NewAILibrary(router)
	env.RegisterLibrary("llmr.ai", aiLib.GetLibrary())
	if mcpServer != nil {
		mcpLib := NewMCPLibrary(mcpServer)
		env.RegisterLibrary("llmr.mcp", mcpLib.GetLibrary())
	}
}

// setupScriptlingEnvironmentWithAIAndResult configures a Scriptling environment with result tracking
func setupScriptlingEnvironmentWithAIAndResult(env *scriptling.Scriptling, router *Router, mcpServer *MCPServer, mcpLib *MCPLibrary) {
	setupScriptlingEnvironment(env)
	aiLib := NewAILibrary(router)
	env.RegisterLibrary("llmr.ai", aiLib.GetLibrary())
	if mcpLib != nil {
		env.RegisterLibrary("llmr.mcp", mcpLib.GetLibrary())
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
	server := mcp.NewServer("llmrouter", "1.0.0")
	server.SetInstructions(`This server provides AI completion with tool calling support and Scriptling execution capabilities.
Use execute_code for custom Scriptling/Python code execution.`)

	mcpServer := &MCPServer{
		server:        server,
		config:        config,
		logger:        logger,
		router:        router,
		toolsPath:     config.Scriptling.ToolsPath,
		librariesPath: config.Scriptling.LibrariesPath,
	}

	if err := mcpServer.initializeScriptling(); err != nil {
		return nil, fmt.Errorf("failed to initialize scriptling: %w", err)
	}

	if err := mcpServer.registerBuiltinTools(); err != nil {
		return nil, fmt.Errorf("failed to register builtin tools: %w", err)
	}

	// Connect to remote MCP servers
	for _, remoteServer := range config.MCP.RemoteServers {
		var auth mcp.AuthProvider
		if remoteServer.Token != "" {
			auth = mcp.NewBearerTokenAuth(remoteServer.Token)
		}

		client := mcp.NewClient(remoteServer.URL, auth, remoteServer.Namespace)

		if remoteServer.ToolVisibility == "ondemand" {
			if err := server.RegisterRemoteServerOnDemand(client); err != nil {
				logger.Warn("failed to connect to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "error", err)
			} else {
				logger.Info("connected to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "visibility", "ondemand")
			}
		} else {
			if err := server.RegisterRemoteServer(client); err != nil {
				logger.Warn("failed to connect to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "error", err)
			} else {
				logger.Info("connected to remote MCP server", "namespace", remoteServer.Namespace, "url", remoteServer.URL, "visibility", "native")
			}
		}
	}

	return mcpServer, nil
}

// initializeScriptling sets up the Scriptling environment
func (m *MCPServer) initializeScriptling() error {
	m.scriptling = scriptling.New()
	setupScriptlingEnvironmentWithAI(m.scriptling, m.router, m)
	m.setupOnDemandLibraryLoading(m.scriptling)
	return nil
}

// registerBuiltinTools registers built-in tools like execute_code
func (m *MCPServer) registerBuiltinTools() error {
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

	m.logger.Info("registered execute_code tool")
	return nil
}

// executeScriptToolFromPath reads the script from disk and executes it
func (m *MCPServer) executeScriptToolFromPath(scriptPath string, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script file %s: %w", scriptPath, err)
	}
	return m.executeScriptTool(string(content), req)
}

// executeScriptTool executes a tool script with arguments
func (m *MCPServer) executeScriptTool(scriptContent string, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	env := scriptling.New()
	mcpLib := NewMCPLibrary(m)
	setupScriptlingEnvironmentWithAIAndResult(env, m.router, m, mcpLib)
	m.setupOnDemandLibraryLoading(env)

	args := make(map[string]interface{})
	for key, value := range req.Args() {
		args[key] = value
	}

	mcpLib.SetArgs(args)

	for k, v := range args {
		if setErr := env.SetVar(k, scriptling.FromGo(v)); setErr != nil {
			log.Error("failed to set variable in scriptling environment", "key", k, "error", setErr)
		}
	}

	result, err := env.Eval(scriptContent)
	output := env.GetOutput()

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

// HandleRequest handles HTTP requests to the MCP server.
// The tool mode is determined from the X-MCP-Tool-Mode header or tool_mode query parameter.
// With session management enabled, the mode is stored in the session during initialize.
// Native-visibility tools from providers appear in tools/list in normal mode.
// In discovery mode (X-MCP-Tool-Mode: discovery), only tool_search and execute_tool are visible.
func (m *MCPServer) HandleRequest(w http.ResponseWriter, r *http.Request) {
	nativeProvider := NewNativeScriptToolProvider(m)
	onDemandProvider := NewOnDemandScriptToolProvider(m)

	// Start with providers attached - the MCP server handles mode from headers/session
	ctx := mcp.WithToolProviders(r.Context(), nativeProvider)

	// Add ondemand provider if there are any ondemand tools
	onDemandTools, _ := onDemandProvider.GetTools(r.Context())
	if len(onDemandTools) > 0 {
		ctx = mcp.WithOnDemandToolProviders(ctx, onDemandProvider)
	}

	m.server.HandleRequest(w, r.WithContext(ctx))
}
