package types

// Configuration types

type Config struct {
	Server        ServerConfig        `json:"server"`
	Logging       LoggingConfig       `json:"logging"`
	Providers     []ProviderConfig    `json:"providers"`
	MCP           MCPConfig           `json:"mcp"`
	Scriptling    ScriptlingConfig    `json:"scriptling"`
	Responses     ResponsesConfig     `json:"responses"`
	Conversations ConversationsConfig `json:"conversations"`
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
	Name            string   `json:"name"`
	BaseURL         string   `json:"base_url"`
	Token           string   `json:"token"`
	Enabled         bool     `json:"enabled"`
	Models          []string `json:"models,omitempty"`
	Allowlist       []string `json:"allowlist,omitempty"`
	Denylist        []string `json:"denylist,omitempty"`
	NativeResponses bool     `json:"native_responses,omitempty"`
}

type MCPConfig struct {
	RemoteServers []MCPRemoteServerConfig `json:"remote_servers,omitempty"`
}

type MCPRemoteServerConfig struct {
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
	Token     string `json:"token,omitempty"`
	Hidden    bool   `json:"hidden,omitempty"`
}

type ScriptlingConfig struct {
	ToolsPath     string `json:"tools_path,omitempty"`
	LibrariesPath string `json:"libraries_path,omitempty"`
}

type ResponsesConfig struct {
	StoragePath string `json:"storage_path,omitempty"`
	TTLDays     int    `json:"ttl_days,omitempty"`
}

type ConversationsConfig struct {
	StoragePath string `json:"storage_path,omitempty"`
	TTLDays     int    `json:"ttl_days,omitempty"`
}
