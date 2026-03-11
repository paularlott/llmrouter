package storage

import (
	"fmt"
	"time"

	"github.com/paularlott/snapshotkv"
)

type Store struct {
	db      *snapshotkv.DB
	memory  bool
	ttl     time.Duration
	convTTL time.Duration
}

func NewStore(path string, conversationTTL time.Duration) (*Store, error) {
	if path == "" {
		// Memory-only mode - no persistence
		return &Store{
			memory:  true,
			convTTL: conversationTTL,
		}, nil
	}

	db, err := snapshotkv.Open(path, &snapshotkv.Config{
		TTLCleanupInterval: 5 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &Store{
		db:      db,
		convTTL: conversationTTL,
	}, nil
}

func (s *Store) IsMemory() bool { return s.memory }

func (s *Store) RunGC() error {
	// snapshotkv handles its own cleanup via TTL
	return nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) NewConversationStorage() ConversationStorage {
	if s.memory {
		return NewMemoryConversationStorage()
	}
	return NewSnapshotConversationStorage(s.db, s.convTTL)
}

func (s *Store) NewMCPStorage() MCPStorage {
	if s.memory {
		return NewMemoryMCPStorage()
	}
	return NewSnapshotMCPStorage(s.db)
}
