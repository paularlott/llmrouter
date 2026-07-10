package types

// Configuration types

type Config struct {
	Server        ServerConfig        `json:"server"`
	Logging       LoggingConfig       `json:"logging"`
	Providers     []ProviderConfig    `json:"providers"`
	MCP           MCPConfig           `json:"mcp"`
	Storage       StorageConfig       `json:"storage"`
	Responses     ResponsesConfig     `json:"responses"`
	Conversations ConversationsConfig `json:"conversations"`
	RoutesDir     string              `json:"routes_dir,omitempty" toml:"routes_dir"` // directory of smart-router <model>.toml/.py pairs
	Scripting     ScriptingConfig     `json:"scripting"`
	Chat          ChatConfig          `json:"chat"`
}

type ServerConfig struct {
	Host          string `json:"host" toml:"host"`
	Port          int    `json:"port" toml:"port"`
	Token         string `json:"token,omitempty" toml:"token"`
	AdminPassword string `json:"admin_password,omitempty" toml:"admin_password"` // If set, enables admin UI + chat
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type ProviderConfig struct {
	Name           string              `json:"name" toml:"name"`
	Provider       string              `json:"provider" toml:"provider"`           // openai | claude | gemini | ollama | mistral | zai
	BaseURL        string              `json:"base_url,omitempty" toml:"base_url"` // optional override
	Token          string              `json:"token" toml:"token"`
	Enabled        bool                `json:"enabled" toml:"enabled"`
	Weight         float64             `json:"weight,omitempty" toml:"weight"`                   // 0.0-2.0, default 1.0; higher = preferred
	Models         []string            `json:"models,omitempty" toml:"models"`                   // if set, use these models instead of querying the provider
	ModelAllowlist []string            `json:"model_allowlist,omitempty" toml:"model_allowlist"` // if set, only these models are used from auto-discovery
	Tags           []string            `json:"tags,omitempty" toml:"tags"`                       // arbitrary tags for routing scripts
	ModelTags      map[string][]string `json:"model_tags,omitempty" toml:"model_tags"`           // model_id -> tags
	ModelDenylist  []string            `json:"model_denylist,omitempty" toml:"model_denylist"`   // models to exclude from auto-discovery
	ModelAliases   map[string]string   `json:"model_aliases,omitempty" toml:"model_aliases"`     // alias -> real model name
}

type MCPConfig struct {
	RemoteServers           []MCPRemoteServerConfig `json:"remote_servers,omitempty"`             // Remote MCP server connections
	ToolCacheRefreshMinutes int                     `json:"tool_cache_refresh_minutes,omitempty"` // Auto-refresh tool cache interval (0 = disabled)
}

type MCPRemoteServerConfig struct {
	Namespace         string   `json:"namespace" toml:"namespace"`
	URL               string   `json:"url" toml:"url"`
	Command           string   `json:"command,omitempty" toml:"command"` // stdio: executable to launch (empty for HTTP)
	Args              []string `json:"args,omitempty" toml:"args"`       // stdio: command-line arguments
	AuthType          string   `json:"auth_type,omitempty" toml:"auth_type"`
	Token             string   `json:"token,omitempty" toml:"token"`
	OAuthClientID     string   `json:"oauth_client_id,omitempty" toml:"oauth_client_id"`
	OAuthTokenURL     string   `json:"oauth_token_url,omitempty" toml:"oauth_token_url"`
	OAuthAccessToken  string   `json:"oauth_access_token,omitempty" toml:"oauth_access_token"`
	OAuthRefreshToken string   `json:"oauth_refresh_token,omitempty" toml:"oauth_refresh_token"`
	ToolVisibility    string   `json:"tool_visibility,omitempty" toml:"tool_visibility"` // "native" (default) or "ondemand"
	ToolAllowlist     []string `json:"tool_allowlist,omitempty" toml:"tool_allowlist"`   // If set, only these tools are enabled
	ToolDenylist      []string `json:"tool_denylist,omitempty" toml:"tool_denylist"`     // If set, these tools are disabled
	StaticServer      bool     `json:"static_server,omitempty" toml:"static_server"`     // If true, server is defined in config (read-only in UI)
	RemoteSearch      bool     `json:"remote_search,omitempty" toml:"remote_search"`     // Delegate tool_search to this remote server
	Notifications     bool     `json:"notifications,omitempty" toml:"notifications"`     // Accept listChanged notifications from this server and propagate them
}

type StorageConfig struct {
	Path string `json:"path,omitempty"` // empty = memory-only
}

type ResponsesConfig struct {
	TTLDays int `json:"ttl_days,omitempty"`
}

type ConversationsConfig struct {
	TTLDays int `json:"ttl_days,omitempty"`
}

// RouterFileConfig is the contents of a single smart-router <model>.toml file.
// The router's trigger model name is the file's stem (no name field).
// A missing companion <model>.py makes the router a pure alias to default_model.
type RouterFileConfig struct {
	DefaultModel string            `json:"default_model,omitempty" toml:"default_model"` // fallback model when the script returns nothing
	Enabled      bool              `json:"enabled" toml:"enabled"`                       // defaults handled by loader (absent = enabled)
	Vars         map[string]string `json:"vars,omitempty" toml:"vars"`                   // exposed to the script as the vars library
}

// ScriptingConfig holds configuration for scriptling-served MCP content.
// Tools, resources and prompts are all optional — set the dir for the kinds
// you want to serve; leave blank to skip.
type ScriptingConfig struct {
	ToolsDir     string   `json:"tools_dir,omitempty"`      // Directory containing .toml/.py tool pairs
	ResourcesDir string   `json:"resources_dir,omitempty"`  // Directory containing static files and resource templates (first segment = URI scheme)
	PromptsDir   string   `json:"prompts_dir,omitempty"`    // Directory containing .toml+.py dynamic prompts or static .md/.txt prompts
	PluginDirs   []string `json:"plugin_dirs,omitempty"`    // Directories containing plugin executables
	LibPaths     []string `json:"lib_paths,omitempty"`      // Additional directories to search for libraries
	ExecScript   bool     `json:"exec_script,omitempty"`    // Register the built-in execute_script MCP tool
}

// ChatConfig configures the /chat UI. When PersonasDir or CommandsDir is
// empty the corresponding feature is disabled (e.g. only the built-in Default
// persona is offered).
type ChatConfig struct {
	PersonasDir string `json:"personas_dir,omitempty" toml:"personas_dir"` // Directory of persona .toml files (empty = Default only)
	CommandsDir string `json:"commands_dir,omitempty" toml:"commands_dir"` // Directory of slash-command .md files (empty = no commands)
}
