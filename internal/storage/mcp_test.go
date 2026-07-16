package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

func tempDirMCP(t *testing.T) string {
	dir, err := os.MkdirTemp("", "mcp-storage-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSnapshotMCPStorage_BasicCRUD(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewMCPStorage()
	ctx := context.Background()

	// Test Create
	server := &MCPServerConfig{
		Namespace:      "test-server",
		URL:            "https://example.com/mcp",
		Token:          "test-token",
		ToolVisibility: "native",
		ToolAllowlist:  []string{"tool1", "tool2"},
	}

	err = storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Verify timestamps were set
	if server.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if server.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}

	// Test Get
	retrieved, err := storage.Get(ctx, "test-server")
	if err != nil {
		t.Fatalf("Failed to get MCP server: %v", err)
	}

	if retrieved.Namespace != "test-server" {
		t.Errorf("Expected namespace 'test-server', got %s", retrieved.Namespace)
	}
	if retrieved.URL != "https://example.com/mcp" {
		t.Errorf("Expected URL 'https://example.com/mcp', got %s", retrieved.URL)
	}
	if retrieved.Token != "test-token" {
		t.Errorf("Expected token 'test-token', got %s", retrieved.Token)
	}
	if retrieved.ToolVisibility != "native" {
		t.Errorf("Expected visibility 'native', got %s", retrieved.ToolVisibility)
	}
	if len(retrieved.ToolAllowlist) != 2 {
		t.Errorf("Expected 2 allowlist items, got %d", len(retrieved.ToolAllowlist))
	}

	// Test List
	servers, err := storage.List(ctx)
	if err != nil {
		t.Fatalf("Failed to list MCP servers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(servers))
	}

	// Test Update
	server.URL = "https://updated.example.com/mcp"
	server.ToolDenylist = []string{"tool3"}
	err = storage.Update(ctx, server)
	if err != nil {
		t.Fatalf("Failed to update MCP server: %v", err)
	}

	updated, err := storage.Get(ctx, "test-server")
	if err != nil {
		t.Fatalf("Failed to get updated MCP server: %v", err)
	}
	if updated.URL != "https://updated.example.com/mcp" {
		t.Errorf("Expected updated URL, got %s", updated.URL)
	}
	if len(updated.ToolDenylist) != 1 {
		t.Errorf("Expected 1 denylist item, got %d", len(updated.ToolDenylist))
	}

	// Test Delete
	err = storage.Delete(ctx, "test-server")
	if err != nil {
		t.Fatalf("Failed to delete MCP server: %v", err)
	}

	_, err = storage.Get(ctx, "test-server")
	if err == nil {
		t.Error("Expected error when getting deleted server")
	}
}

func TestSnapshotMCPStorage_ToggleTool(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewMCPStorage()
	ctx := context.Background()

	// Create a server
	server := &MCPServerConfig{
		Namespace: "toggle-test",
		URL:       "https://example.com/mcp",
	}
	err = storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Toggle tool off
	err = storage.ToggleTool(ctx, "toggle-test", "tool1", false)
	if err != nil {
		t.Fatalf("Failed to toggle tool off: %v", err)
	}

	retrieved, err := storage.Get(ctx, "toggle-test")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if len(retrieved.DisabledTools) != 1 {
		t.Errorf("Expected 1 disabled tool, got %d", len(retrieved.DisabledTools))
	}
	if retrieved.DisabledTools[0] != "tool1" {
		t.Errorf("Expected 'tool1' in disabled tools, got %v", retrieved.DisabledTools)
	}

	// Toggle another tool off
	err = storage.ToggleTool(ctx, "toggle-test", "tool2", false)
	if err != nil {
		t.Fatalf("Failed to toggle tool2 off: %v", err)
	}

	retrieved, err = storage.Get(ctx, "toggle-test")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if len(retrieved.DisabledTools) != 2 {
		t.Errorf("Expected 2 disabled tools, got %d", len(retrieved.DisabledTools))
	}

	// Toggle tool back on
	err = storage.ToggleTool(ctx, "toggle-test", "tool1", true)
	if err != nil {
		t.Fatalf("Failed to toggle tool on: %v", err)
	}

	retrieved, err = storage.Get(ctx, "toggle-test")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if len(retrieved.DisabledTools) != 1 {
		t.Errorf("Expected 1 disabled tool after re-enabling, got %d", len(retrieved.DisabledTools))
	}
	if retrieved.DisabledTools[0] != "tool2" {
		t.Errorf("Expected 'tool2' in disabled tools, got %v", retrieved.DisabledTools)
	}
}

func TestSnapshotMCPStorage_DuplicateNamespace(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewMCPStorage()
	ctx := context.Background()

	server := &MCPServerConfig{
		Namespace: "duplicate-test",
		URL:       "https://example.com/mcp",
	}

	err = storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Try to create with same namespace
	duplicate := &MCPServerConfig{
		Namespace: "duplicate-test",
		URL:       "https://other.example.com/mcp",
	}
	err = storage.Create(ctx, duplicate)
	if err == nil {
		t.Error("Expected error when creating duplicate namespace")
	}
}

func TestSnapshotMCPStorage_UpdateNonExistent(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewMCPStorage()
	ctx := context.Background()

	server := &MCPServerConfig{
		Namespace: "nonexistent",
		URL:       "https://example.com/mcp",
	}

	err = storage.Update(ctx, server)
	if err == nil {
		t.Error("Expected error when updating non-existent server")
	}
}

func TestSnapshotMCPStorage_DefaultVisibility(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewMCPStorage()
	ctx := context.Background()

	// Create server without specifying visibility
	server := &MCPServerConfig{
		Namespace: "default-visibility",
		URL:       "https://example.com/mcp",
	}
	err = storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	retrieved, err := storage.Get(ctx, "default-visibility")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if retrieved.ToolVisibility != "native" {
		t.Errorf("Expected default visibility 'native', got %s", retrieved.ToolVisibility)
	}
}

func TestMemoryMCPStorage_BasicCRUD(t *testing.T) {
	storage := NewMemoryMCPStorage()
	ctx := context.Background()

	// Test Create
	server := &MCPServerConfig{
		Namespace:      "memory-test",
		URL:            "https://example.com/mcp",
		Token:          "test-token",
		ToolVisibility: "ondemand",
	}

	err := storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Test Get
	retrieved, err := storage.Get(ctx, "memory-test")
	if err != nil {
		t.Fatalf("Failed to get MCP server: %v", err)
	}

	if retrieved.Namespace != "memory-test" {
		t.Errorf("Expected namespace 'memory-test', got %s", retrieved.Namespace)
	}

	// Test List
	servers, err := storage.List(ctx)
	if err != nil {
		t.Fatalf("Failed to list MCP servers: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("Expected 1 server, got %d", len(servers))
	}

	// Test Delete
	err = storage.Delete(ctx, "memory-test")
	if err != nil {
		t.Fatalf("Failed to delete MCP server: %v", err)
	}

	_, err = storage.Get(ctx, "memory-test")
	if err == nil {
		t.Error("Expected error when getting deleted server")
	}
}

func TestMemoryMCPStorage_ToggleTool(t *testing.T) {
	storage := NewMemoryMCPStorage()
	ctx := context.Background()

	server := &MCPServerConfig{
		Namespace: "memory-toggle",
		URL:       "https://example.com/mcp",
	}
	err := storage.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Toggle tool off
	err = storage.ToggleTool(ctx, "memory-toggle", "tool1", false)
	if err != nil {
		t.Fatalf("Failed to toggle tool: %v", err)
	}

	retrieved, err := storage.Get(ctx, "memory-toggle")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if len(retrieved.DisabledTools) != 1 {
		t.Errorf("Expected 1 disabled tool, got %d", len(retrieved.DisabledTools))
	}

	// Toggle tool on
	err = storage.ToggleTool(ctx, "memory-toggle", "tool1", true)
	if err != nil {
		t.Fatalf("Failed to toggle tool: %v", err)
	}

	retrieved, err = storage.Get(ctx, "memory-toggle")
	if err != nil {
		t.Fatalf("Failed to get server: %v", err)
	}
	if len(retrieved.DisabledTools) != 0 {
		t.Errorf("Expected 0 disabled tools, got %d", len(retrieved.DisabledTools))
	}
}

func TestMCPServerConfig_IsToolEnabled(t *testing.T) {
	tests := []struct {
		name      string
		config    *MCPServerConfig
		toolName  string
		expected  bool
	}{
		{
			name: "no restrictions",
			config: &MCPServerConfig{
				Namespace: "test",
			},
			toolName: "tool1",
			expected: true,
		},
		{
			name: "in allowlist",
			config: &MCPServerConfig{
				Namespace:     "test",
				ToolAllowlist: []string{"tool1", "tool2"},
			},
			toolName: "tool1",
			expected: true,
		},
		{
			name: "not in allowlist",
			config: &MCPServerConfig{
				Namespace:     "test",
				ToolAllowlist: []string{"tool1", "tool2"},
			},
			toolName: "tool3",
			expected: false,
		},
		{
			name: "in denylist",
			config: &MCPServerConfig{
				Namespace:    "test",
				ToolDenylist: []string{"tool1"},
			},
			toolName: "tool1",
			expected: false,
		},
		{
			name: "not in denylist",
			config: &MCPServerConfig{
				Namespace:    "test",
				ToolDenylist: []string{"tool1"},
			},
			toolName: "tool2",
			expected: true,
		},
		{
			name: "disabled via toggle",
			config: &MCPServerConfig{
				Namespace:     "test",
				DisabledTools: []string{"tool1"},
			},
			toolName: "tool1",
			expected: false,
		},
		{
			name: "allowlist takes precedence over denylist",
			config: &MCPServerConfig{
				Namespace:     "test",
				ToolAllowlist: []string{"tool1"},
				ToolDenylist:  []string{"tool1"},
			},
			toolName: "tool1",
			expected: true,
		},
		{
			name: "not in allowlist but in denylist",
			config: &MCPServerConfig{
				Namespace:     "test",
				ToolAllowlist: []string{"tool1"},
				ToolDenylist:  []string{"tool2"},
			},
			toolName: "tool2",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.IsToolEnabled(tt.toolName)
			if result != tt.expected {
				t.Errorf("IsToolEnabled(%q) = %v, expected %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestMCPServerConfig_ToJSON(t *testing.T) {
	server := &MCPServerConfig{
		Namespace:      "json-test",
		URL:            "https://example.com/mcp",
		Token:          "secret-token",
		ToolVisibility: "native",
		ToolAllowlist:  []string{"tool1"},
		ToolDenylist:   []string{"tool2"},
		DisabledTools:  []string{"tool3"},
		CreatedAt:      1234567890,
		UpdatedAt:      1234567891,
	}

	// Without token
	json := server.ToJSON(false)
	if _, ok := json["token"]; ok {
		t.Error("Token should not be included when includeToken is false")
	}

	// With token
	json = server.ToJSON(true)
	if token, ok := json["token"].(string); !ok || token != "secret-token" {
		t.Error("Token should be included when includeToken is true")
	}

	// Check required fields
	if json["namespace"] != "json-test" {
		t.Error("Namespace should be included")
	}
	if json["url"] != "https://example.com/mcp" {
		t.Error("URL should be included")
	}
}

func TestSnapshotMCPStorage_Persistence(t *testing.T) {
	dir := tempDirMCP(t)
	ttl := 24 * time.Hour

	// Create first store and save data
	store1, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	storage1 := store1.NewMCPStorage()
	ctx := context.Background()

	server := &MCPServerConfig{
		Namespace:      "persistent",
		URL:            "https://example.com/mcp",
		Token:          "persistent-token",
		ToolVisibility: "ondemand",
		ToolAllowlist:  []string{"tool1", "tool2"},
	}
	err = storage1.Create(ctx, server)
	if err != nil {
		t.Fatalf("Failed to create MCP server: %v", err)
	}

	// Close and create new store
	store1.Close()

	store2, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}
	defer store2.Close()

	storage2 := store2.NewMCPStorage()

	// Verify data persisted
	retrieved, err := storage2.Get(ctx, "persistent")
	if err != nil {
		t.Fatalf("Failed to get persisted server: %v", err)
	}

	if retrieved.Namespace != "persistent" {
		t.Errorf("Expected namespace 'persistent', got %s", retrieved.Namespace)
	}
	if retrieved.URL != "https://example.com/mcp" {
		t.Errorf("Expected URL 'https://example.com/mcp', got %s", retrieved.URL)
	}
	if retrieved.Token != "persistent-token" {
		t.Errorf("Expected token 'persistent-token', got %s", retrieved.Token)
	}
	if len(retrieved.ToolAllowlist) != 2 {
		t.Errorf("Expected 2 allowlist items, got %d", len(retrieved.ToolAllowlist))
	}
}

// TestMCPStorage_StdioFieldsRoundTrip verifies that stdio-specific fields
// (command, args, env, notifications) survive a Create -> Get -> Update -> Get
// cycle across both storage backends. This guards against the field-enumeration
// in saveServer/parseMCPServerConfig/copyMCPServerConfig silently dropping
// fields that aren't explicitly listed.
func TestMCPStorage_StdioFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()

	stores := map[string]MCPStorage{
		"snapshot": func() MCPStorage {
			dir := tempDirMCP(t)
			ttl := 24 * time.Hour
			store, err := NewStore(dir, ttl)
			if err != nil {
				t.Fatalf("Failed to create store: %v", err)
			}
			t.Cleanup(func() { store.Close() })
			return store.NewMCPStorage()
		}(),
		"memory": NewMemoryMCPStorage(),
	}

	for name, storage := range stores {
		t.Run(name, func(t *testing.T) {
			server := &MCPServerConfig{
				Namespace:      "stdio-" + name,
				Command:        "npx",
				Args:           []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"},
				Env:            []string{"FS_ROOT=/data", "LOG_LEVEL=debug"},
				Enabled:        true,
				Notifications:  true,
				RemoteSearch:   true,
				ToolVisibility: "native",
			}

			if err := storage.Create(ctx, server); err != nil {
				t.Fatalf("Create: %v", err)
			}

			got, err := storage.Get(ctx, server.Namespace)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Command != "npx" {
				t.Errorf("Command: got %q, want %q", got.Command, "npx")
			}
			if len(got.Args) != 3 || got.Args[0] != "-y" {
				t.Errorf("Args: got %+v", got.Args)
			}
			if len(got.Env) != 2 || got.Env[0] != "FS_ROOT=/data" {
				t.Errorf("Env: got %+v", got.Env)
			}
			if !got.Notifications {
				t.Error("Notifications: got false, want true")
			}
			if !got.RemoteSearch {
				t.Error("RemoteSearch: got false, want true")
			}
			if !got.Enabled {
				t.Error("Enabled: got false, want true")
			}

			// Update must also preserve the stdio fields.
			got.Env = []string{"FS_ROOT=/other", "DEBUG=1", "EXTRA=x"}
			got.Notifications = false
			if err := storage.Update(ctx, got); err != nil {
				t.Fatalf("Update: %v", err)
			}

			updated, err := storage.Get(ctx, server.Namespace)
			if err != nil {
				t.Fatalf("Get after update: %v", err)
			}
			if updated.Command != "npx" {
				t.Errorf("Command after update: got %q, want %q", updated.Command, "npx")
			}
			if len(updated.Args) != 3 {
				t.Errorf("Args after update: got %+v", updated.Args)
			}
			if len(updated.Env) != 3 || updated.Env[2] != "EXTRA=x" {
				t.Errorf("Env after update: got %+v", updated.Env)
			}
			if updated.Notifications {
				t.Error("Notifications after update: got true, want false")
			}
		})
	}
}
