package snapshotkv

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Config holds database configuration
type Config struct {
	// Path is the base directory for storage
	Path string
	// MaxSnapshots is the maximum number of snapshot files to keep (default: 5)
	MaxSnapshots int
	// SaveDebounce is the debounce interval for saves (default: 10s)
	SaveDebounce time.Duration
	// DisableCompression disables gzip compression for snapshots (default: false, compression enabled)
	DisableCompression bool
	// TTLCleanupInterval is the interval for TTL cleanup (default: 5m, 0 to disable)
	TTLCleanupInterval time.Duration
	// Codec is the encoder/decoder (default: MsgpackCodec)
	Codec Codec
}

// DB is the main database structure
type DB struct {
	mu        sync.RWMutex
	data      map[string]map[string]any
	config    *Config
	snapshots *snapshotManager
	blobs     *blobManager
	closed    bool

	// Transaction mode
	inTransaction    bool
	transactionMu    sync.Mutex
	transactionBlobs []string // Blobs created during transaction

	// Debounced save
	dirty       bool
	saveTimer   *time.Timer
	saveTimerMu sync.Mutex

	// TTL cleanup
	ttlStopCh chan struct{}
	ttlWg     sync.WaitGroup
}

// Open opens or creates a database at the given path
func Open(path string, cfg *Config) (*DB, error) {
	// Apply defaults
	if cfg == nil {
		cfg = &Config{}
	}

	cfg.Path = cmp.Or(cfg.Path, path)
	cfg.MaxSnapshots = cmp.Or(cfg.MaxSnapshots, 5)
	cfg.SaveDebounce = cmp.Or(cfg.SaveDebounce, 10*time.Second)
	cfg.TTLCleanupInterval = cmp.Or(cfg.TTLCleanupInterval, 5*time.Minute)
	if cfg.Codec == nil {
		cfg.Codec = MsgpackCodec{}
	}

	// Create directories
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	snapshotsPath := filepath.Join(cfg.Path, "snapshots")
	blobsPath := filepath.Join(cfg.Path, "blobs")

	// Create subdirectories
	if err := os.MkdirAll(snapshotsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshots directory: %w", err)
	}
	if err := os.MkdirAll(blobsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create blobs directory: %w", err)
	}

	db := &DB{
		data:      make(map[string]map[string]any),
		config:    cfg,
		snapshots: newSnapshotManager(snapshotsPath, cfg.Codec, !cfg.DisableCompression, cfg.MaxSnapshots),
		blobs:     newBlobManager(blobsPath),
		ttlStopCh: make(chan struct{}),
	}

	// Clean up temp files from incomplete writes
	db.snapshots.cleanupTempFiles()
	db.blobs.cleanupTempFiles()

	// Load existing snapshot
	data, err := db.snapshots.load()
	if err != nil && err != ErrNoSnapshots {
		return nil, fmt.Errorf("failed to load snapshot: %w", err)
	}
	db.data = data

	// Clean up orphaned blobs in background
	go db.cleanupOrphanedBlobs()

	// Start TTL cleanup goroutine
	if cfg.TTLCleanupInterval > 0 {
		db.ttlWg.Add(1)
		go db.ttlCleanupLoop()
	}

	return db, nil
}

// Close closes the database and stops background goroutines
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}

	db.closed = true

	// Stop TTL cleanup
	if db.ttlStopCh != nil {
		close(db.ttlStopCh)
	}
	db.ttlWg.Wait()

	// Stop debounce timer and flush immediately
	db.stopTimerAndFlush()

	return nil
}

// Save forces an immediate snapshot save
func (db *DB) Save() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return ErrDatabaseClosed
	}

	return db.saveLocked()
}

// saveLocked saves a snapshot (must be called with lock held)
func (db *DB) saveLocked() error {
	return db.snapshots.save(db.data)
}

// checkClosed returns an error if the database is closed
func (db *DB) checkClosed() error {
	if db.closed {
		return ErrDatabaseClosed
	}
	return nil
}

// markDirty marks the database as dirty and schedules a debounced save
func (db *DB) markDirty() {
	if db.inTransaction {
		return
	}

	db.saveTimerMu.Lock()
	defer db.saveTimerMu.Unlock()

	// Already dirty, timer running
	if db.dirty {
		return
	}

	db.dirty = true

	// If debounce is 0, save immediately
	if db.config.SaveDebounce == 0 {
		db.saveLocked()
		db.dirty = false
		return
	}

	// Start debounce timer
	if db.saveTimer == nil {
		db.saveTimer = time.AfterFunc(db.config.SaveDebounce, db.debouncedSave)
	} else {
		db.saveTimer.Reset(db.config.SaveDebounce)
	}
}

// debouncedSave is called after the debounce timer fires
func (db *DB) debouncedSave() {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed || !db.dirty {
		return
	}

	db.saveLocked()
	db.dirty = false
}

// stopTimerAndFlush cancels any pending save and saves immediately if dirty
func (db *DB) stopTimerAndFlush() {
	db.saveTimerMu.Lock()
	if db.saveTimer != nil {
		db.saveTimer.Stop()
	}
	db.saveTimerMu.Unlock()

	if db.dirty {
		db.saveLocked()
		db.dirty = false
	}
}

// Get retrieves a value by key
func (db *DB) Get(key string) (map[string]any, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if err := db.checkClosed(); err != nil {
		return nil, err
	}

	data, exists := db.data[key]
	if !exists {
		return nil, ErrNotFound
	}

	// Return a copy without internal fields
	return db.stripInternalFields(data), nil
}

// stripInternalFields removes internal fields from a document
func (db *DB) stripInternalFields(data map[string]any) map[string]any {
	// Pre-size to avoid reallocation (minus 2 for potential internal fields)
	result := make(map[string]any, len(data)-2)
	for k, v := range data {
		if k != internalBlobRef && k != internalTTLExpres {
			result[k] = v
		}
	}
	return result
}

// Set stores a value without expiry
func (db *DB) Set(key string, value map[string]any) error {
	return db.SetEx(key, value, 0)
}

// SetEx stores a value with TTL
func (db *DB) SetEx(key string, value map[string]any, ttl time.Duration) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	return db.setLocked(key, value, ttl)
}

// setLocked stores a value (must be called with lock held)
func (db *DB) setLocked(key string, value map[string]any, ttl time.Duration) error {
	// Copy value to avoid external mutations
	data := db.copyMap(value)

	// Set TTL if provided (store as nanoseconds for precision)
	if ttl > 0 {
		data[internalTTLExpres] = time.Now().Add(ttl).UnixNano()
	}

	db.data[key] = data

	// Mark dirty for debounced save
	db.markDirty()

	return nil
}

// copyMap creates a deep copy of a map
func (db *DB) copyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Delete removes a key and its associated blob
func (db *DB) Delete(key string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	return db.deleteLocked(key)
}

// deleteLocked removes a key (must be called with lock held)
func (db *DB) deleteLocked(key string) error {
	data, exists := db.data[key]
	if !exists {
		return nil // Already gone
	}

	// Delete associated blob if exists
	if blobRef, ok := data[internalBlobRef].(string); ok && blobRef != "" {
		db.blobs.deleteBlob(blobRef)
	}

	delete(db.data, key)

	// Mark dirty for debounced save
	db.markDirty()

	return nil
}

// FindKeysByPrefix returns all keys matching a prefix
func (db *DB) FindKeysByPrefix(prefix string) []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil
	}

	var keys []string
	for key := range db.data {
		if len(prefix) == 0 || len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}

	// Sort keys for consistent ordering
	slices.Sort(keys)
	return keys
}

// SetWithBlob stores a value with blob data (no expiry)
func (db *DB) SetWithBlob(key string, value map[string]any, blob []byte) error {
	return db.SetWithBlobEx(key, value, blob, 0)
}

// SetWithBlobEx stores a value with blob data and TTL
func (db *DB) SetWithBlobEx(key string, value map[string]any, blob []byte, ttl time.Duration) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	// Write blob
	blobRef, err := db.blobs.writeBlob(blob)
	if err != nil {
		return fmt.Errorf("failed to write blob: %w", err)
	}

	// Track blob for transaction rollback
	if db.inTransaction {
		db.transactionBlobs = append(db.transactionBlobs, blobRef)
	}

	// Copy value and add blob reference
	data := db.copyMap(value)
	data[internalBlobRef] = blobRef

	if ttl > 0 {
		data[internalTTLExpres] = time.Now().Add(ttl).UnixNano()
	}

	db.data[key] = data

	// Mark dirty for debounced save
	db.markDirty()

	return nil
}

// GetBlob retrieves the blob data for a key
func (db *DB) GetBlob(key string) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if err := db.checkClosed(); err != nil {
		return nil, err
	}

	data, exists := db.data[key]
	if !exists {
		return nil, ErrNotFound
	}

	blobRef, ok := data[internalBlobRef].(string)
	if !ok || blobRef == "" {
		return nil, ErrNoBlob
	}

	return db.blobs.readBlob(blobRef)
}

// UpdateBlob updates the blob data for an existing key
func (db *DB) UpdateBlob(key string, blob []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	data, exists := db.data[key]
	if !exists {
		return ErrNotFound
	}

	// Delete old blob if exists
	if oldBlobRef, ok := data[internalBlobRef].(string); ok && oldBlobRef != "" {
		db.blobs.deleteBlob(oldBlobRef)
	}

	// Write new blob
	blobRef, err := db.blobs.writeBlob(blob)
	if err != nil {
		return fmt.Errorf("failed to write blob: %w", err)
	}

	// Track blob for transaction rollback
	if db.inTransaction {
		db.transactionBlobs = append(db.transactionBlobs, blobRef)
	}

	// Update data with new blob reference
	data[internalBlobRef] = blobRef

	// Mark dirty for debounced save
	db.markDirty()

	return nil
}

// BeginTransaction starts transaction mode (defers save until Commit)
func (db *DB) BeginTransaction() error {
	db.transactionMu.Lock()
	defer db.transactionMu.Unlock()

	db.mu.RLock()
	defer db.mu.RUnlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	if db.inTransaction {
		return ErrAlreadyInTransaction
	}

	db.inTransaction = true
	db.transactionBlobs = nil
	return nil
}

// Commit exits transaction mode and saves immediately
func (db *DB) Commit() error {
	db.transactionMu.Lock()
	defer db.transactionMu.Unlock()

	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.checkClosed(); err != nil {
		return err
	}

	if !db.inTransaction {
		return ErrNotInTransaction
	}

	db.inTransaction = false
	db.transactionBlobs = nil // Clear tracked blobs - they're now committed

	// Stop timer and save immediately
	db.stopTimerAndFlush()
	return nil
}

// Rollback exits transaction mode without saving and deletes any blobs created
func (db *DB) Rollback() error {
	db.transactionMu.Lock()
	defer db.transactionMu.Unlock()

	if !db.inTransaction {
		return ErrNotInTransaction
	}

	// Delete any blobs created during the transaction
	for _, blobRef := range db.transactionBlobs {
		db.blobs.deleteBlob(blobRef)
	}

	db.inTransaction = false
	db.transactionBlobs = nil
	db.dirty = false
	return nil
}

// ttlCleanupLoop runs the TTL cleanup goroutine
func (db *DB) ttlCleanupLoop() {
	defer db.ttlWg.Done()

	ticker := time.NewTicker(db.config.TTLCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-db.ttlStopCh:
			return
		case <-ticker.C:
			db.cleanupExpired()
		}
	}
}

// cleanupExpired removes expired keys and their blobs
func (db *DB) cleanupExpired() {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return
	}

	now := time.Now().UnixNano()
	var deleted bool

	for key, data := range db.data {
		// Handle different integer types from deserialization
		var expiresAt int64
		switch v := data[internalTTLExpres].(type) {
		case int64:
			expiresAt = v
		case int:
			expiresAt = int64(v)
		case uint64:
			expiresAt = int64(v)
		case int32:
			expiresAt = int64(v)
		case uint32:
			expiresAt = int64(v)
		default:
			continue // No TTL or unknown type
		}

		if expiresAt < now {
			// Delete blob if exists
			if blobRef, ok := data[internalBlobRef].(string); ok && blobRef != "" {
				db.blobs.deleteBlob(blobRef)
			}
			delete(db.data, key)
			deleted = true
		}
	}

	// Mark dirty if any keys were deleted
	if deleted {
		db.markDirty()
	}
}

// cleanupOrphanedBlobs removes blobs on disk that aren't referenced by any key
func (db *DB) cleanupOrphanedBlobs() {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return
	}

	// Build set of referenced blob IDs
	referenced := make(map[string]struct{})
	for _, data := range db.data {
		if blobRef, ok := data[internalBlobRef].(string); ok && blobRef != "" {
			referenced[blobRef] = struct{}{}
		}
	}

	// Delete orphaned blobs
	db.blobs.deleteOrphanedBlobs(referenced)
}
