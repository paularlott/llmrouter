package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/paularlott/snapshotkv"
)

// StoredProviderConfig represents a provider stored in KV storage.
// Mirrors types.ProviderConfig but adds timestamps for storage metadata.
type StoredProviderConfig struct {
	Name           string              `json:"name"`
	Provider       string              `json:"provider"`
	BaseURL        string              `json:"base_url,omitempty"`
	Token          string              `json:"token,omitempty"`
	Enabled        bool                `json:"enabled"`
	Weight         float64             `json:"weight,omitempty"`
	Models         []string            `json:"models,omitempty"`
	ModelAllowlist []string            `json:"model_allowlist,omitempty"`
	Tags           []string            `json:"tags,omitempty"`
	ModelTags      map[string][]string `json:"model_tags,omitempty"`
	ModelDenylist  []string            `json:"model_denylist,omitempty"`
	ModelAliases   map[string]string   `json:"model_aliases,omitempty"`
	CreatedAt      int64               `json:"created_at"`
	UpdatedAt      int64               `json:"updated_at"`
}

// ProviderStorage defines the interface for provider storage.
type ProviderStorage interface {
	Create(ctx context.Context, provider *StoredProviderConfig) error
	Get(ctx context.Context, name string) (*StoredProviderConfig, error)
	List(ctx context.Context) ([]*StoredProviderConfig, error)
	Update(ctx context.Context, provider *StoredProviderConfig) error
	Rename(ctx context.Context, oldName, newName string) error
	Delete(ctx context.Context, name string) error
}

const providerKeyPrefix = "providers:"

// SnapshotProviderStorage implements ProviderStorage using snapshotkv.
type SnapshotProviderStorage struct {
	db *snapshotkv.DB
	mu sync.RWMutex
}

func NewSnapshotProviderStorage(db *snapshotkv.DB) *SnapshotProviderStorage {
	return &SnapshotProviderStorage{db: db}
}

func (s *SnapshotProviderStorage) providerKey(name string) string {
	return providerKeyPrefix + name
}

func (s *SnapshotProviderStorage) Create(ctx context.Context, provider *StoredProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.providerKey(provider.Name)
	if _, err := s.db.Get(key); err == nil {
		return fmt.Errorf("provider with name %q already exists", provider.Name)
	}

	now := time.Now().Unix()
	provider.CreatedAt = now
	provider.UpdatedAt = now
	return s.saveProvider(key, provider)
}

func (s *SnapshotProviderStorage) Get(ctx context.Context, name string) (*StoredProviderConfig, error) {
	data, err := s.db.Get(s.providerKey(name))
	if err != nil {
		return nil, fmt.Errorf("provider not found")
	}
	m, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid data type for provider config")
	}
	return parseProviderConfig(m)
}

func (s *SnapshotProviderStorage) List(ctx context.Context) ([]*StoredProviderConfig, error) {
	keys := s.db.FindKeysByPrefix(providerKeyPrefix)
	providers := make([]*StoredProviderConfig, 0, len(keys))
	for _, key := range keys {
		data, err := s.db.Get(key)
		if err != nil {
			continue
		}
		m, ok := data.(map[string]any)
		if !ok {
			continue
		}
		p, err := parseProviderConfig(m)
		if err != nil {
			continue
		}
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	return providers, nil
}

func (s *SnapshotProviderStorage) Update(ctx context.Context, provider *StoredProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.providerKey(provider.Name)
	if _, err := s.db.Get(key); err != nil {
		return fmt.Errorf("provider not found")
	}

	provider.UpdatedAt = time.Now().Unix()
	return s.saveProvider(key, provider)
}

// Rename changes a provider's key from oldName to newName, preserving the
// stored config (including CreatedAt) and updating the embedded Name field.
func (s *SnapshotProviderStorage) Rename(ctx context.Context, oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldKey := s.providerKey(oldName)
	newKey := s.providerKey(newName)

	data, err := s.db.Get(oldKey)
	if err != nil {
		return fmt.Errorf("provider not found")
	}
	if _, err := s.db.Get(newKey); err == nil {
		return fmt.Errorf("provider with name %q already exists", newName)
	}

	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid data type for provider config")
	}
	m["name"] = newName
	m["updated_at"] = time.Now().Unix()

	if err := s.db.Set(newKey, m); err != nil {
		return err
	}
	return s.db.Delete(oldKey)
}

func (s *SnapshotProviderStorage) Delete(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Delete(s.providerKey(name))
}

func (s *SnapshotProviderStorage) saveProvider(key string, provider *StoredProviderConfig) error {
	data := map[string]any{
		"name":            provider.Name,
		"provider":        provider.Provider,
		"base_url":        provider.BaseURL,
		"token":           provider.Token,
		"enabled":         provider.Enabled,
		"weight":          provider.Weight,
		"models":          provider.Models,
		"model_allowlist": provider.ModelAllowlist,
		"tags":            provider.Tags,
		"model_tags":      provider.ModelTags,
		"model_denylist":  provider.ModelDenylist,
		"model_aliases":   provider.ModelAliases,
		"created_at":      provider.CreatedAt,
		"updated_at":      provider.UpdatedAt,
	}
	return s.db.Set(key, data)
}

func parseProviderConfig(m map[string]any) (*StoredProviderConfig, error) {
	p := &StoredProviderConfig{}
	if v, ok := m["name"].(string); ok {
		p.Name = v
	}
	if v, ok := m["provider"].(string); ok {
		p.Provider = v
	}
	if v, ok := m["base_url"].(string); ok {
		p.BaseURL = v
	}
	if v, ok := m["token"].(string); ok {
		p.Token = v
	}
	if v, ok := m["enabled"].(bool); ok {
		p.Enabled = v
	}
	if v, ok := m["weight"].(float64); ok {
		p.Weight = v
	}
	p.Models = toStringSlice(m["models"])
	p.ModelAllowlist = toStringSlice(m["model_allowlist"])
	p.Tags = toStringSlice(m["tags"])
	p.ModelDenylist = toStringSlice(m["model_denylist"])
	if v, ok := m["created_at"].(float64); ok {
		p.CreatedAt = int64(v)
	}
	if v, ok := m["updated_at"].(float64); ok {
		p.UpdatedAt = int64(v)
	}
	return p, nil
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	s, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, item := range s {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// MemoryProviderStorage implements ProviderStorage using in-memory storage.
type MemoryProviderStorage struct {
	providers map[string]*StoredProviderConfig
	mu        sync.RWMutex
}

func NewMemoryProviderStorage() *MemoryProviderStorage {
	return &MemoryProviderStorage{providers: make(map[string]*StoredProviderConfig)}
}

func (s *MemoryProviderStorage) Create(ctx context.Context, provider *StoredProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.providers[provider.Name]; exists {
		return fmt.Errorf("provider with name %q already exists", provider.Name)
	}
	now := time.Now().Unix()
	provider.CreatedAt = now
	provider.UpdatedAt = now
	s.providers[provider.Name] = provider
	return nil
}

func (s *MemoryProviderStorage) Get(ctx context.Context, name string) (*StoredProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not found")
	}
	return p, nil
}

func (s *MemoryProviderStorage) List(ctx context.Context) ([]*StoredProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*StoredProviderConfig, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryProviderStorage) Update(ctx context.Context, provider *StoredProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[provider.Name]; !ok {
		return fmt.Errorf("provider not found")
	}
	provider.UpdatedAt = time.Now().Unix()
	s.providers[provider.Name] = provider
	return nil
}

// Rename changes a provider's key from oldName to newName, preserving the
// stored config (including CreatedAt) and updating the embedded Name field.
func (s *MemoryProviderStorage) Rename(ctx context.Context, oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.providers[oldName]
	if !ok {
		return fmt.Errorf("provider not found")
	}
	if _, exists := s.providers[newName]; exists {
		return fmt.Errorf("provider with name %q already exists", newName)
	}

	delete(s.providers, oldName)
	p.Name = newName
	p.UpdatedAt = time.Now().Unix()
	s.providers[newName] = p
	return nil
}

func (s *MemoryProviderStorage) Delete(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.providers, name)
	return nil
}
