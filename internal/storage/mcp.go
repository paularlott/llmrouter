package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/paularlott/snapshotkv"
)

// MCPServerConfig represents a stored MCP server configuration
type MCPServerConfig struct {
	Namespace         string   `json:"namespace"`
	URL               string   `json:"url"`
	Command           string   `json:"command,omitempty"`   // stdio: executable to launch (empty for HTTP)
	Args              []string `json:"args,omitempty"`      // stdio: command-line arguments
	AuthType          string   `json:"auth_type,omitempty"` // "bearer" (default) or "oauth2"
	Token             string   `json:"token,omitempty"`
	OAuthClientID     string   `json:"oauth_client_id,omitempty"`
	OAuthTokenURL     string   `json:"oauth_token_url,omitempty"`
	OAuthAccessToken  string   `json:"oauth_access_token,omitempty"`
	OAuthRefreshToken string   `json:"oauth_refresh_token,omitempty"`
	Enabled           bool     `json:"enabled"`         // Server is active (default true)
	ToolVisibility    string   `json:"tool_visibility"` // "native" (default) or "ondemand"
	ToolAllowlist     []string `json:"tool_allowlist,omitempty"`
	ToolDenylist      []string `json:"tool_denylist,omitempty"`
	DisabledTools     []string `json:"disabled_tools,omitempty"` // Tools disabled via UI toggle
	RemoteSearch      bool     `json:"remote_search,omitempty"`  // Delegate tool_search to this remote
	Notifications     bool     `json:"notifications,omitempty"`  // Accept listChanged notifications from this server and propagate them
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

// MCPStorage defines the interface for MCP server storage
type MCPStorage interface {
	Create(ctx context.Context, server *MCPServerConfig) error
	Get(ctx context.Context, namespace string) (*MCPServerConfig, error)
	List(ctx context.Context) ([]*MCPServerConfig, error)
	Update(ctx context.Context, server *MCPServerConfig) error
	Delete(ctx context.Context, namespace string) error
	ToggleTool(ctx context.Context, namespace, toolName string, enabled bool) error
}

const mcpKeyPrefix = "mcp_servers:"

// SnapshotMCPStorage implements MCPStorage using snapshotkv
type SnapshotMCPStorage struct {
	db *snapshotkv.DB
	mu sync.RWMutex
}

// NewSnapshotMCPStorage creates a new snapshotkv-backed MCP storage
func NewSnapshotMCPStorage(db *snapshotkv.DB) *SnapshotMCPStorage {
	return &SnapshotMCPStorage{db: db}
}

func (s *SnapshotMCPStorage) mcpKey(namespace string) string {
	return mcpKeyPrefix + namespace
}

func (s *SnapshotMCPStorage) Create(ctx context.Context, server *MCPServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.mcpKey(server.Namespace)

	// Check if already exists
	_, err := s.db.Get(key)
	if err == nil {
		return fmt.Errorf("MCP server with namespace %q already exists", server.Namespace)
	}

	now := time.Now().Unix()
	server.CreatedAt = now
	server.UpdatedAt = now

	// Set default visibility
	if server.ToolVisibility == "" {
		server.ToolVisibility = "native"
	}

	return s.saveServer(key, server)
}

func (s *SnapshotMCPStorage) Get(ctx context.Context, namespace string) (*MCPServerConfig, error) {
	key := s.mcpKey(namespace)

	data, err := s.db.Get(key)
	if err != nil {
		if err == snapshotkv.ErrNotFound {
			return nil, fmt.Errorf("MCP server not found")
		}
		return nil, err
	}

	m, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid data type for MCP server config")
	}

	return parseMCPServerConfig(m)
}

func (s *SnapshotMCPStorage) List(ctx context.Context) ([]*MCPServerConfig, error) {
	keys := s.db.FindKeysByPrefix(mcpKeyPrefix)
	servers := make([]*MCPServerConfig, 0, len(keys))

	for _, key := range keys {
		data, err := s.db.Get(key)
		if err != nil {
			continue
		}

		m, ok := data.(map[string]any)
		if !ok {
			continue
		}

		server, err := parseMCPServerConfig(m)
		if err != nil {
			continue
		}

		servers = append(servers, server)
	}

	return servers, nil
}

func (s *SnapshotMCPStorage) Update(ctx context.Context, server *MCPServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.mcpKey(server.Namespace)

	// Check if exists
	_, err := s.db.Get(key)
	if err != nil {
		return fmt.Errorf("MCP server not found")
	}

	server.UpdatedAt = time.Now().Unix()

	return s.saveServer(key, server)
}

func (s *SnapshotMCPStorage) saveServer(key string, server *MCPServerConfig) error {
	data := map[string]any{
		"namespace":           server.Namespace,
		"url":                 server.URL,
		"auth_type":           server.AuthType,
		"token":               server.Token,
		"oauth_client_id":     server.OAuthClientID,
		"oauth_token_url":     server.OAuthTokenURL,
		"oauth_access_token":  server.OAuthAccessToken,
		"oauth_refresh_token": server.OAuthRefreshToken,
		"enabled":             server.Enabled,
		"tool_visibility":     server.ToolVisibility,
		"tool_allowlist":      server.ToolAllowlist,
		"tool_denylist":       server.ToolDenylist,
		"disabled_tools":      server.DisabledTools,
		"remote_search":       server.RemoteSearch,
		"created_at":          server.CreatedAt,
		"updated_at":          server.UpdatedAt,
	}

	return s.db.Set(key, data)
}

func (s *SnapshotMCPStorage) Delete(ctx context.Context, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.mcpKey(namespace)
	return s.db.Delete(key)
}

// ToggleTool enables or disables a specific tool for an MCP server
// This is a lazy write - it only updates the disabled_tools list
func (s *SnapshotMCPStorage) ToggleTool(ctx context.Context, namespace, toolName string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.mcpKey(namespace)

	// Get existing config
	data, err := s.db.Get(key)
	if err != nil {
		return fmt.Errorf("MCP server not found")
	}

	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid data type for MCP server config")
	}

	server, err := parseMCPServerConfig(m)
	if err != nil {
		return err
	}

	// Update disabled tools list
	disabledSet := make(map[string]bool)
	for _, t := range server.DisabledTools {
		disabledSet[t] = true
	}

	if enabled {
		// Remove from disabled list
		delete(disabledSet, toolName)
	} else {
		// Add to disabled list
		disabledSet[toolName] = true
	}

	// Convert back to slice
	server.DisabledTools = make([]string, 0, len(disabledSet))
	for t := range disabledSet {
		server.DisabledTools = append(server.DisabledTools, t)
	}

	server.UpdatedAt = time.Now().Unix()

	return s.saveServer(key, server)
}

// MemoryMCPStorage implements MCPStorage using in-memory storage
type MemoryMCPStorage struct {
	servers map[string]*MCPServerConfig
	mu      sync.RWMutex
}

// NewMemoryMCPStorage creates a new in-memory MCP storage
func NewMemoryMCPStorage() *MemoryMCPStorage {
	return &MemoryMCPStorage{
		servers: make(map[string]*MCPServerConfig),
	}
}

func (s *MemoryMCPStorage) Create(ctx context.Context, server *MCPServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.servers[server.Namespace]; exists {
		return fmt.Errorf("MCP server with namespace %q already exists", server.Namespace)
	}

	now := time.Now().Unix()
	server.CreatedAt = now
	server.UpdatedAt = now

	if server.ToolVisibility == "" {
		server.ToolVisibility = "native"
	}

	// Make copies of slices
	server = copyMCPServerConfig(server)
	s.servers[server.Namespace] = server
	return nil
}

func (s *MemoryMCPStorage) Get(ctx context.Context, namespace string) (*MCPServerConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	server, exists := s.servers[namespace]
	if !exists {
		return nil, fmt.Errorf("MCP server not found")
	}

	return copyMCPServerConfig(server), nil
}

func (s *MemoryMCPStorage) List(ctx context.Context) ([]*MCPServerConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	servers := make([]*MCPServerConfig, 0, len(s.servers))
	for _, server := range s.servers {
		servers = append(servers, copyMCPServerConfig(server))
	}
	return servers, nil
}

func (s *MemoryMCPStorage) Update(ctx context.Context, server *MCPServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.servers[server.Namespace]; !exists {
		return fmt.Errorf("MCP server not found")
	}

	server.UpdatedAt = time.Now().Unix()
	s.servers[server.Namespace] = copyMCPServerConfig(server)
	return nil
}

func (s *MemoryMCPStorage) Delete(ctx context.Context, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.servers, namespace)
	return nil
}

func (s *MemoryMCPStorage) ToggleTool(ctx context.Context, namespace, toolName string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	server, exists := s.servers[namespace]
	if !exists {
		return fmt.Errorf("MCP server not found")
	}

	disabledSet := make(map[string]bool)
	for _, t := range server.DisabledTools {
		disabledSet[t] = true
	}

	if enabled {
		delete(disabledSet, toolName)
	} else {
		disabledSet[toolName] = true
	}

	server.DisabledTools = make([]string, 0, len(disabledSet))
	for t := range disabledSet {
		server.DisabledTools = append(server.DisabledTools, t)
	}

	server.UpdatedAt = time.Now().Unix()
	return nil
}

// Helper functions

func parseMCPServerConfig(data map[string]any) (*MCPServerConfig, error) {
	server := &MCPServerConfig{}

	if v, ok := data["namespace"].(string); ok {
		server.Namespace = v
	}
	if v, ok := data["url"].(string); ok {
		server.URL = v
	}
	if v, ok := data["auth_type"].(string); ok {
		server.AuthType = v
	}
	if v, ok := data["token"].(string); ok {
		server.Token = v
	}
	if v, ok := data["oauth_client_id"].(string); ok {
		server.OAuthClientID = v
	}
	if v, ok := data["oauth_token_url"].(string); ok {
		server.OAuthTokenURL = v
	}
	if v, ok := data["oauth_access_token"].(string); ok {
		server.OAuthAccessToken = v
	}
	if v, ok := data["oauth_refresh_token"].(string); ok {
		server.OAuthRefreshToken = v
	}
	// Default to true if enabled field is not present (backwards compatibility)
	server.Enabled = true
	if v, ok := data["enabled"].(bool); ok {
		server.Enabled = v
	}
	if v, ok := data["tool_visibility"].(string); ok {
		server.ToolVisibility = v
	}
	server.ToolAllowlist = parseStringSlice(data["tool_allowlist"])
	server.ToolDenylist = parseStringSlice(data["tool_denylist"])
	server.DisabledTools = parseStringSlice(data["disabled_tools"])
	if v, ok := data["remote_search"].(bool); ok {
		server.RemoteSearch = v
	}
	if v, ok := data["created_at"].(int64); ok {
		server.CreatedAt = v
	}
	if v, ok := data["created_at"].(int); ok {
		server.CreatedAt = int64(v)
	}
	if v, ok := data["updated_at"].(int64); ok {
		server.UpdatedAt = v
	}
	if v, ok := data["updated_at"].(int); ok {
		server.UpdatedAt = int64(v)
	}

	return server, nil
}

// parseStringSlice converts an interface to a string slice
// Handles both []string and []interface{} types
func parseStringSlice(v any) []string {
	if v == nil {
		return nil
	}

	// Try as []string first
	if slice, ok := v.([]string); ok {
		return slice
	}

	// Try as []interface{}
	if slice, ok := v.([]any); ok {
		result := make([]string, 0, len(slice))
		for _, item := range slice {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}

	return nil
}

func copyMCPServerConfig(server *MCPServerConfig) *MCPServerConfig {
	result := &MCPServerConfig{
		Namespace:         server.Namespace,
		URL:               server.URL,
		AuthType:          server.AuthType,
		Token:             server.Token,
		OAuthClientID:     server.OAuthClientID,
		OAuthTokenURL:     server.OAuthTokenURL,
		OAuthAccessToken:  server.OAuthAccessToken,
		OAuthRefreshToken: server.OAuthRefreshToken,
		ToolVisibility:    server.ToolVisibility,
		Enabled:           server.Enabled,
		RemoteSearch:      server.RemoteSearch,
		CreatedAt:         server.CreatedAt,
		UpdatedAt:         server.UpdatedAt,
	}

	if server.ToolAllowlist != nil {
		result.ToolAllowlist = make([]string, len(server.ToolAllowlist))
		copy(result.ToolAllowlist, server.ToolAllowlist)
	}
	if server.ToolDenylist != nil {
		result.ToolDenylist = make([]string, len(server.ToolDenylist))
		copy(result.ToolDenylist, server.ToolDenylist)
	}
	if server.DisabledTools != nil {
		result.DisabledTools = make([]string, len(server.DisabledTools))
		copy(result.DisabledTools, server.DisabledTools)
	}

	return result
}

// IsToolEnabled checks if a tool is enabled based on allowlist, denylist, and disabled_tools
func (c *MCPServerConfig) IsToolEnabled(toolName string) bool {
	// First check allowlist
	if len(c.ToolAllowlist) > 0 {
		for _, t := range c.ToolAllowlist {
			if t == toolName {
				return true
			}
		}
		return false
	}

	// Check denylist
	for _, t := range c.ToolDenylist {
		if t == toolName {
			return false
		}
	}

	// Check disabled tools (from UI toggles)
	for _, t := range c.DisabledTools {
		if t == toolName {
			return false
		}
	}

	return true
}

// ToJSON returns a JSON representation safe for API responses
func (c *MCPServerConfig) ToJSON(includeToken bool) map[string]any {
	result := map[string]any{
		"namespace":       c.Namespace,
		"url":             c.URL,
		"auth_type":       c.AuthType,
		"tool_visibility": c.ToolVisibility,
		"created_at":      c.CreatedAt,
		"updated_at":      c.UpdatedAt,
	}

	if includeToken {
		result["token"] = c.Token
		result["oauth_client_id"] = c.OAuthClientID
		result["oauth_token_url"] = c.OAuthTokenURL
		result["oauth_access_token"] = c.OAuthAccessToken
		result["oauth_refresh_token"] = c.OAuthRefreshToken
	}

	if len(c.ToolAllowlist) > 0 {
		result["tool_allowlist"] = c.ToolAllowlist
	}
	if len(c.ToolDenylist) > 0 {
		result["tool_denylist"] = c.ToolDenylist
	}
	if len(c.DisabledTools) > 0 {
		result["disabled_tools"] = c.DisabledTools
	}

	return result
}

// MarshalJSON implements json.Marshaler for MCPServerConfig
func (c *MCPServerConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.ToJSON(true))
}
