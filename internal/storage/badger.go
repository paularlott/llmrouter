package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// BadgerDB implementation
type BadgerStorage struct {
	db  *badger.DB
	ttl time.Duration
}

func NewBadgerStorage(path string, ttl time.Duration) (*BadgerStorage, error) {
	opts := badger.DefaultOptions(path).WithLogger(nil) // Disable badger logging
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &BadgerStorage{
		db:  db,
		ttl: ttl,
	}, nil
}

func (s *BadgerStorage) Store(ctx context.Context, response *StoredResponse) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		entry := badger.NewEntry([]byte("response:"+response.ID), data)
		if s.ttl > 0 {
			entry = entry.WithTTL(s.ttl)
		}
		return txn.SetEntry(entry)
	})
}

func (s *BadgerStorage) Get(ctx context.Context, id string) (*StoredResponse, error) {
	var response StoredResponse

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("response:" + id))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &response)
		})
	})

	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("response not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get response: %w", err)
	}

	return &response, nil
}

func (s *BadgerStorage) List(ctx context.Context, filter ResponseFilter) ([]StoredResponse, error) {
	var responses []StoredResponse

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte("response:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var response StoredResponse
				if err := json.Unmarshal(val, &response); err != nil {
					return err
				}
				responses = append(responses, response)
				return nil
			})
			if err != nil {
				return err
			}

			// Apply limit if specified
			if filter.Limit > 0 && len(responses) >= filter.Limit {
				break
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list responses: %w", err)
	}

	return responses, nil
}

func (s *BadgerStorage) Delete(ctx context.Context, id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte("response:" + id))
	})
}

func (s *BadgerStorage) UpdateStatus(ctx context.Context, id string, status ResponseStatus) error {
	return s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("response:" + id))
		if err != nil {
			return err
		}

		var response StoredResponse
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &response)
		})
		if err != nil {
			return err
		}

		response.Status = status
		response.UpdatedAt = time.Now()

		data, err := json.Marshal(response)
		if err != nil {
			return err
		}

		entry := badger.NewEntry([]byte("response:"+id), data)
		if s.ttl > 0 {
			entry = entry.WithTTL(s.ttl)
		}
		return txn.SetEntry(entry)
	})
}

func (s *BadgerStorage) RunGC() error {
	return s.db.RunValueLogGC(0.5)
}

func (s *BadgerStorage) Close() error {
	return s.db.Close()
}