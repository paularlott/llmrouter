package snapshotkv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// blobManager handles blob file operations with sharding
type blobManager struct {
	basePath string
	mu       sync.Mutex
}

// newBlobManager creates a new blob manager
func newBlobManager(basePath string) *blobManager {
	return &blobManager{
		basePath: basePath,
	}
}

// generateBlobID generates a UUID v7 for blob identification
func (b *blobManager) generateBlobID() string {
	// Generate UUID v7 (time-sortable)
	id := uuid.Must(uuid.NewV7())
	return id.String()
}

// shardPath returns the sharded directory path for a blob ID
// Uses last 3 hex chars for better distribution: blobs/0ab/<uuid>.bin
// UUID v7 format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars)
// Last 3 hex chars are at positions 32,33,34 (after removing hyphens at 8,13,18,23)
func (b *blobManager) shardPath(blobID string) string {
	// Fast path: extract last 3 chars directly from UUID string
	// UUID format is fixed, last segment is 12 chars starting at position 24
	var shard string
	if len(blobID) >= 36 {
		shard = blobID[33:36] // Last 3 chars of the UUID
	} else if len(blobID) >= 3 {
		shard = blobID[len(blobID)-3:]
	} else {
		shard = "000"
	}
	return filepath.Join(b.basePath, shard)
}

// blobFilePath returns the full path for a blob file
func (b *blobManager) blobFilePath(blobID string) string {
	return filepath.Join(b.shardPath(blobID), blobID+".bin")
}

// writeBlob writes blob data to disk and returns the blob ID
func (b *blobManager) writeBlob(data []byte) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	blobID := b.generateBlobID()
	shardPath := b.shardPath(blobID)
	filePath := b.blobFilePath(blobID)

	// Create shard directory if needed
	if err := os.MkdirAll(shardPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create blob directory: %w", err)
	}

	// Write to temp file first (atomic write)
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write blob: %w", err)
	}

	// Rename to final path
	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("failed to rename blob: %w", err)
	}

	return blobID, nil
}

// readBlob reads blob data from disk
func (b *blobManager) readBlob(blobID string) ([]byte, error) {
	filePath := b.blobFilePath(blobID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrBlobNotFound
		}
		return nil, fmt.Errorf("failed to read blob: %w", err)
	}
	return data, nil
}

// deleteBlob removes a blob file from disk
func (b *blobManager) deleteBlob(blobID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	filePath := b.blobFilePath(blobID)
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete blob: %w", err)
	}
	return nil
}

// listBlobs returns all blob IDs currently on disk
func (b *blobManager) listBlobs() (map[string]struct{}, error) {
	blobs := make(map[string]struct{})

	entries, err := os.ReadDir(b.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return blobs, nil
		}
		return nil, fmt.Errorf("failed to read blobs directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Read shard subdirectory
		shardPath := filepath.Join(b.basePath, entry.Name())
		files, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			name := file.Name()
			// Extract blob ID from filename (format: <uuid>.bin)
			if blobID, ok := strings.CutSuffix(name, ".bin"); ok {
				blobs[blobID] = struct{}{}
			}
		}
	}

	return blobs, nil
}

// deleteOrphanedBlobs removes blobs that exist on disk but aren't in the referenced set
func (b *blobManager) deleteOrphanedBlobs(referenced map[string]struct{}) (int, error) {
	onDisk, err := b.listBlobs()
	if err != nil {
		return 0, err
	}

	var deleted int
	for blobID := range onDisk {
		if _, exists := referenced[blobID]; !exists {
			if err := b.deleteBlob(blobID); err != nil {
				return deleted, err
			}
			deleted++
		}
	}

	return deleted, nil
}

// cleanupTempFiles removes any temp files left from incomplete writes
func (b *blobManager) cleanupTempFiles() error {
	entries, err := os.ReadDir(b.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Read shard subdirectory
		shardPath := filepath.Join(b.basePath, entry.Name())
		files, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			name := file.Name()
			// Remove .tmp files (incomplete writes)
			if strings.HasSuffix(name, ".tmp") {
				filePath := filepath.Join(shardPath, name)
				os.Remove(filePath)
			}
		}
	}

	return nil
}
