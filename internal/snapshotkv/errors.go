package snapshotkv

import "errors"

var (
	// ErrNotFound is returned when a key does not exist
	ErrNotFound = errors.New("key not found")

	// ErrBlobNotFound is returned when a blob file does not exist
	ErrBlobNotFound = errors.New("blob not found")

	// ErrNoBlob is returned when trying to get a blob from a key without one
	ErrNoBlob = errors.New("key has no associated blob")

	// ErrDatabaseClosed is returned when operations are called after Close()
	ErrDatabaseClosed = errors.New("database is closed")

	// ErrNotInTransaction is returned when Commit/Rollback is called outside transaction mode
	ErrNotInTransaction = errors.New("not in transaction mode")

	// ErrAlreadyInTransaction is returned when BeginTransaction is called while already in transaction mode
	ErrAlreadyInTransaction = errors.New("already in transaction mode")

	// ErrNoSnapshots is returned when no valid snapshots can be loaded
	ErrNoSnapshots = errors.New("no valid snapshots found")
)

// Internal field names - these are stripped from data returned to callers
const (
	internalBlobRef   = "_blob_ref"
	internalTTLExpres = "_ttl_expires"
)

// Backward compatibility aliases
var (
	ErrNotInBatch        = ErrNotInTransaction
	ErrAlreadyInBatch    = ErrAlreadyInTransaction
)
