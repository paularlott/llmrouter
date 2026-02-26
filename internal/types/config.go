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
	Host  string `json:"host"`
	Port  int    `json:"port"`
	Token string `json:"token,omitempty"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type ProviderConfig struct {
	Name     string            `json:"name"`
	Provider string            `json:"provider"`           // openai | claude | gemini | ollama | mistral | zai
	BaseURL  string            `json:"base_url,omitempty"` // optional override
	Token    string            `json:"token"`
	Enabled  bool              `json:"enabled"`
	Weight   float64           `json:"weight,omitempty"`   // 0.0-2.0, default 1.0; higher = preferred
	Models   []string          `json:"models,omitempty"`   // empty = auto-discover; required for claude
	Tags     []string          `json:"tags,omitempty"`     // arbitrary tags for routing scripts
	ModelTags map[string][]string `json:"model_tags,omitempty"` // model_id -> tags
}

type MCPConfig struct {
	RemoteServers []MCPRemoteServerConfig `json:"remote_servers,omitempty"` // Remote MCP server connections
}

type MCPRemoteServerConfig struct {
	Namespace      string `json:"namespace"`
	URL            string `json:"url"`
	Token          string `json:"token,omitempty"`
	ToolVisibility string `json:"tool_visibility,omitempty"` // "native" (default) or "ondemand"
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
	Enabled      bool   `json:"enabled"`
	Script       string `json:"script,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}
