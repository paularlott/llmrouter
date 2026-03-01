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
	SmartRouting  SmartRoutingConfig  `json:"smart_routing"`
}

type ServerConfig struct {
	Host          string `json:"host" toml:"host"`
	Port          int    `json:"port" toml:"port"`
	Token         string `json:"token,omitempty" toml:"token"`
	AdminPassword string `json:"admin_password,omitempty" toml:"admin_password"` // If set, enables admin UI
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type ProviderConfig struct {
	Name     string            `json:"name" toml:"name"`
	Provider string            `json:"provider" toml:"provider"`           // openai | claude | gemini | ollama | mistral | zai
	BaseURL  string            `json:"base_url,omitempty" toml:"base_url"` // optional override
	Token    string            `json:"token" toml:"token"`
	Enabled  bool              `json:"enabled" toml:"enabled"`
	Weight   float64           `json:"weight,omitempty" toml:"weight"`     // 0.0-2.0, default 1.0; higher = preferred
	Models        []string            `json:"models,omitempty" toml:"models"`                    // if set, use these models instead of querying the provider
	ModelAllowlist []string           `json:"model_allowlist,omitempty" toml:"model_allowlist"`  // if set, only these models are used from auto-discovery
	Tags          []string            `json:"tags,omitempty" toml:"tags"`                        // arbitrary tags for routing scripts
	ModelTags     map[string][]string `json:"model_tags,omitempty" toml:"model_tags"`            // model_id -> tags
	ModelDenylist []string            `json:"model_denylist,omitempty" toml:"model_denylist"`    // models to exclude from auto-discovery
}

type MCPConfig struct {
	RemoteServers []MCPRemoteServerConfig `json:"remote_servers,omitempty"` // Remote MCP server connections
}

type MCPRemoteServerConfig struct {
	Namespace           string   `json:"namespace" toml:"namespace"`
	URL                 string   `json:"url" toml:"url"`
	AuthType            string   `json:"auth_type,omitempty" toml:"auth_type"`
	Token               string   `json:"token,omitempty" toml:"token"`
	OAuthClientID       string   `json:"oauth_client_id,omitempty" toml:"oauth_client_id"`
	OAuthTokenURL       string   `json:"oauth_token_url,omitempty" toml:"oauth_token_url"`
	OAuthAccessToken    string   `json:"oauth_access_token,omitempty" toml:"oauth_access_token"`
	OAuthRefreshToken   string   `json:"oauth_refresh_token,omitempty" toml:"oauth_refresh_token"`
	ToolVisibility      string   `json:"tool_visibility,omitempty" toml:"tool_visibility"` // "native" (default) or "ondemand"
	ToolAllowlist       []string `json:"tool_allowlist,omitempty" toml:"tool_allowlist"`   // If set, only these tools are enabled
	ToolDenylist        []string `json:"tool_denylist,omitempty" toml:"tool_denylist"`     // If set, these tools are disabled
	StaticServer        bool     `json:"static_server,omitempty" toml:"static_server"`     // If true, server is defined in config (read-only in UI)
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

type SmartRoutingConfig struct {
	Enabled      bool              `json:"enabled"`
	Script       string            `json:"script,omitempty"`
	DefaultModel string            `json:"default_model,omitempty"`
	Vars         map[string]string `json:"vars,omitempty"` // key-value pairs exposed to routing scripts
	LibDir       string            `json:"libdir,omitempty"` // directory of .py script libraries auto-loaded into every VM
}
