package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/paularlott/snapshotkv"
)

// StoredPersona represents a persona created via the admin UI. Mirrors the
// fields of a lmchatkit persona TOML file (name, description, system_prompt,
// default_model, [params]) plus a stable ID and storage metadata.
type StoredPersona struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	SystemPrompt string                 `json:"system_prompt,omitempty"`
	DefaultModel string                 `json:"default_model,omitempty"`
	Params       map[string]interface{} `json:"params,omitempty"`
	CreatedAt    int64                  `json:"created_at"`
	UpdatedAt    int64                  `json:"updated_at"`
}

// PersonaStorage defines the interface for persona storage. As with
// ProviderStorage, the admin UI treats config-file personas as read-only and
// only stored personas support CRUD.
type PersonaStorage interface {
	Create(ctx context.Context, persona *StoredPersona) error
	Get(ctx context.Context, id string) (*StoredPersona, error)
	List(ctx context.Context) ([]*StoredPersona, error)
	Update(ctx context.Context, persona *StoredPersona) error
	Delete(ctx context.Context, id string) error
}

const personaKeyPrefix = "personas:"

// SnapshotPersonaStorage implements PersonaStorage using snapshotkv. Personas
// are serialised as JSON blobs (Params is a free-form map, so JSON avoids the
// type-assertion soup that map[string]any storage would require on read-back).
type SnapshotPersonaStorage struct {
	db *snapshotkv.DB
	mu sync.RWMutex
}

func NewSnapshotPersonaStorage(db *snapshotkv.DB) *SnapshotPersonaStorage {
	return &SnapshotPersonaStorage{db: db}
}

func (s *SnapshotPersonaStorage) personaKey(id string) string {
	return personaKeyPrefix + id
}

func (s *SnapshotPersonaStorage) Create(ctx context.Context, persona *StoredPersona) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.personaKey(persona.ID)
	if _, err := s.db.Get(key); err == nil {
		return fmt.Errorf("persona with id %q already exists", persona.ID)
	}

	now := time.Now().Unix()
	persona.CreatedAt = now
	persona.UpdatedAt = now
	return s.savePersona(key, persona)
}

func (s *SnapshotPersonaStorage) Get(ctx context.Context, id string) (*StoredPersona, error) {
	raw, err := s.db.Get(s.personaKey(id))
	if err != nil {
		if err == snapshotkv.ErrNotFound {
			return nil, fmt.Errorf("persona not found")
		}
		return nil, err
	}
	return parseStoredPersona(raw)
}

func (s *SnapshotPersonaStorage) List(ctx context.Context) ([]*StoredPersona, error) {
	keys := s.db.FindKeysByPrefix(personaKeyPrefix)
	personas := make([]*StoredPersona, 0, len(keys))
	for _, key := range keys {
		raw, err := s.db.Get(key)
		if err != nil {
			continue
		}
		p, err := parseStoredPersona(raw)
		if err != nil {
			continue
		}
		personas = append(personas, p)
	}
	sort.Slice(personas, func(i, j int) bool { return personas[i].Name < personas[j].Name })
	return personas, nil
}

func (s *SnapshotPersonaStorage) Update(ctx context.Context, persona *StoredPersona) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.personaKey(persona.ID)
	if _, err := s.db.Get(key); err != nil {
		return fmt.Errorf("persona not found")
	}

	persona.UpdatedAt = time.Now().Unix()
	return s.savePersona(key, persona)
}

func (s *SnapshotPersonaStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Delete(s.personaKey(id))
}

func (s *SnapshotPersonaStorage) savePersona(key string, persona *StoredPersona) error {
	data, err := json.Marshal(persona)
	if err != nil {
		return err
	}
	return s.db.Set(key, data)
}

func parseStoredPersona(raw any) (*StoredPersona, error) {
	data, ok := raw.([]byte)
	if !ok {
		return nil, fmt.Errorf("invalid data type for persona")
	}
	var p StoredPersona
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// MemoryPersonaStorage implements PersonaStorage using in-memory storage.
type MemoryPersonaStorage struct {
	personas map[string]*StoredPersona
	mu       sync.RWMutex
}

func NewMemoryPersonaStorage() *MemoryPersonaStorage {
	return &MemoryPersonaStorage{personas: make(map[string]*StoredPersona)}
}

func (s *MemoryPersonaStorage) Create(ctx context.Context, persona *StoredPersona) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.personas[persona.ID]; exists {
		return fmt.Errorf("persona with id %q already exists", persona.ID)
	}
	now := time.Now().Unix()
	persona.CreatedAt = now
	persona.UpdatedAt = now
	s.personas[persona.ID] = persona
	return nil
}

func (s *MemoryPersonaStorage) Get(ctx context.Context, id string) (*StoredPersona, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.personas[id]
	if !ok {
		return nil, fmt.Errorf("persona not found")
	}
	return p, nil
}

func (s *MemoryPersonaStorage) List(ctx context.Context) ([]*StoredPersona, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*StoredPersona, 0, len(s.personas))
	for _, p := range s.personas {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryPersonaStorage) Update(ctx context.Context, persona *StoredPersona) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.personas[persona.ID]; !ok {
		return fmt.Errorf("persona not found")
	}
	persona.UpdatedAt = time.Now().Unix()
	s.personas[persona.ID] = persona
	return nil
}

func (s *MemoryPersonaStorage) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.personas, id)
	return nil
}
