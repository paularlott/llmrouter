package storage

import (
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// Store is a shared storage instance (BadgerDB or memory) used by all services.
type Store struct {
	db *badger.DB // nil = memory mode
}

// NewStore opens a BadgerDB at path, or returns a memory-only store if path is empty.
func NewStore(path string) (*Store, error) {
	if path == "" {
		return &Store{}, nil
	}
	opts := badger.DefaultOptions(path).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) IsMemory() bool { return s.db == nil }

func (s *Store) RunGC() error {
	if s.db == nil {
		return nil
	}
	return s.db.RunValueLogGC(0.5)
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// NewResponseStorage returns a ResponseStorage backed by this store.
func (s *Store) NewResponseStorage(ttl time.Duration) ResponseStorage {
	if s.db == nil {
		return NewMemoryStorage()
	}
	return &BadgerStorage{db: s.db, ttl: ttl}
}

// NewConversationStorage returns a ConversationStorage backed by this store.
func (s *Store) NewConversationStorage(ttl time.Duration) ConversationStorage {
	if s.db == nil {
		return NewMemoryConversationStorage()
	}
	return newBadgerConversationStorage(s.db, ttl)
}
