package storage

import (
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
)

type Store struct {
	db *badger.DB
}

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

func (s *Store) NewConversationStorage(ttl time.Duration) ConversationStorage {
	if s.db == nil {
		return NewMemoryConversationStorage()
	}
	return newBadgerConversationStorage(s.db, ttl)
}
