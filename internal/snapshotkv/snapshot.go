package snapshotkv

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SnapshotVersion is the current snapshot format version
const SnapshotVersion = 1

// Snapshot represents the on-disk snapshot format
type Snapshot struct {
	Version   int                       `msgpack:"version" json:"version"`
	CreatedAt time.Time                 `msgpack:"created_at" json:"created_at"`
	Data      map[string]map[string]any `msgpack:"data" json:"data"`
}

// snapshotManager handles reading/writing snapshot files
type snapshotManager struct {
	basePath     string
	codec        Codec
	compress     bool
	maxSnapshots int

	mu         sync.Mutex
	lastSecond int64
	counter    uint16
}

// newSnapshotManager creates a new snapshot manager
func newSnapshotManager(basePath string, codec Codec, compress bool, maxSnapshots int) *snapshotManager {
	return &snapshotManager{
		basePath:     basePath,
		codec:        codec,
		compress:     compress,
		maxSnapshots: maxSnapshots,
	}
}

// generateFilename generates a unique snapshot filename using HLC
// Format: data-YYYYMMDD-HHMMSS-CCCC.<ext>.gz (if compressed)
func (s *snapshotManager) generateFilename() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	currentSecond := now.Unix()

	// Reset counter if second changed
	if currentSecond != s.lastSecond {
		s.lastSecond = currentSecond
		s.counter = 0
	} else {
		s.counter++
	}

	timestamp := now.Format("20060102-150405")
	counter := fmt.Sprintf("%04x", s.counter)
	ext := s.codec.Extension()

	if s.compress {
		return fmt.Sprintf("data-%s-%s.%s.gz", timestamp, counter, ext)
	}
	return fmt.Sprintf("data-%s-%s.%s", timestamp, counter, ext)
}

// parseSnapshotTime extracts the timestamp and counter from a snapshot filename
// Returns timestamp, counter, valid
func parseSnapshotTime(filename string) (time.Time, uint16, bool) {
	// Expected format: data-YYYYMMDD-HHMMSS-CCCC.ext or data-YYYYMMDD-HHMMSS-CCCC.ext.gz
	if !strings.HasPrefix(filename, "data-") {
		return time.Time{}, 0, false
	}

	// Remove prefix and extensions
	rest := strings.TrimPrefix(filename, "data-")
	rest = strings.TrimSuffix(rest, ".gz")
	rest = strings.TrimSuffix(rest, ".msgpack")
	rest = strings.TrimSuffix(rest, ".json")

	// Split into parts: YYYYMMDD-HHMMSS-CCCC
	parts := strings.Split(rest, "-")
	if len(parts) != 3 {
		return time.Time{}, 0, false
	}

	// Parse timestamp
	timestampStr := parts[0] + "-" + parts[1]
	t, err := time.Parse("20060102-150405", timestampStr)
	if err != nil {
		return time.Time{}, 0, false
	}

	// Parse counter
	var counter uint16
	_, err = fmt.Sscanf(parts[2], "%04x", &counter)
	if err != nil {
		return time.Time{}, 0, false
	}

	return t, counter, true
}

// Save writes a snapshot to disk
func (s *snapshotManager) save(data map[string]map[string]any) error {
	snapshot := &Snapshot{
		Version:   SnapshotVersion,
		CreatedAt: time.Now(),
		Data:      data,
	}

	// Encode snapshot
	encoded, err := s.codec.Encode(snapshot)
	if err != nil {
		return fmt.Errorf("failed to encode snapshot: %w", err)
	}

	// Generate filename
	filename := s.generateFilename()
	filePath := filepath.Join(s.basePath, filename)

	// Ensure directory exists
	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return fmt.Errorf("failed to create snapshots directory: %w", err)
	}

	// Write to temp file
	tempPath := filePath + ".tmp"
	f, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	var writer io.Writer = f
	if s.compress {
		gz := gzip.NewWriter(f)
		writer = gz
		defer gz.Close()
	}

	if _, err := writer.Write(encoded); err != nil {
		f.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to write snapshot: %w", err)
	}

	if s.compress {
		// Need to close gzip writer before closing file
		if closer, ok := writer.(*gzip.Writer); ok {
			closer.Close()
		}
	}

	f.Close()

	// Rename to final path (atomic)
	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename snapshot: %w", err)
	}

	// Clean up old snapshots
	go s.cleanupOldSnapshots()

	return nil
}

// Load loads the most recent valid snapshot from disk
func (s *snapshotManager) load() (map[string]map[string]any, error) {
	// List snapshot files
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]map[string]any), nil
		}
		return nil, fmt.Errorf("failed to read snapshots directory: %w", err)
	}

	// Filter and sort snapshot files (newest first)
	var snapshots []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "data-") && !strings.HasPrefix(name, ".tmp") {
			snapshots = append(snapshots, name)
		}
	}

	if len(snapshots) == 0 {
		return make(map[string]map[string]any), nil
	}

	// Sort by timestamp and counter (descending)
	sort.Slice(snapshots, func(i, j int) bool {
		t1, c1, ok1 := parseSnapshotTime(snapshots[i])
		t2, c2, ok2 := parseSnapshotTime(snapshots[j])
		if !ok1 || !ok2 {
			return snapshots[i] > snapshots[j] // fallback to string comparison
		}
		if t1.Equal(t2) {
			return c1 > c2
		}
		return t1.After(t2)
	})

	// Try to load each snapshot until one succeeds
	for _, filename := range snapshots {
		data, err := s.loadFile(filename)
		if err == nil {
			return data, nil
		}
		// Continue to next snapshot on error
	}

	return nil, ErrNoSnapshots
}

// loadFile loads a specific snapshot file
func (s *snapshotManager) loadFile(filename string) (map[string]map[string]any, error) {
	filePath := filepath.Join(s.basePath, filename)

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var reader io.Reader = f
	// Only decompress if the file actually has .gz suffix (not based on config)
	if strings.HasSuffix(filename, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	encoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	var snapshot Snapshot
	if err := s.codec.Decode(encoded, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}

	// Validate version
	if snapshot.Version > SnapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version: %d", snapshot.Version)
	}

	return snapshot.Data, nil
}

// cleanupOldSnapshots removes old snapshot files beyond retention limit
func (s *snapshotManager) cleanupOldSnapshots() {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		return
	}

	// Filter and sort snapshot files (oldest first for deletion)
	var snapshots []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "data-") && !strings.HasPrefix(name, ".tmp") {
			snapshots = append(snapshots, name)
		}
	}

	// Sort by timestamp and counter (ascending - oldest first)
	sort.Slice(snapshots, func(i, j int) bool {
		t1, c1, ok1 := parseSnapshotTime(snapshots[i])
		t2, c2, ok2 := parseSnapshotTime(snapshots[j])
		if !ok1 || !ok2 {
			return snapshots[i] < snapshots[j]
		}
		if t1.Equal(t2) {
			return c1 < c2
		}
		return t1.Before(t2)
	})

	// Delete oldest snapshots beyond limit
	for i := 0; i < len(snapshots)-s.maxSnapshots; i++ {
		filePath := filepath.Join(s.basePath, snapshots[i])
		os.Remove(filePath)
	}
}

// cleanupTempFiles removes any temp files left from incomplete writes
func (s *snapshotManager) cleanupTempFiles() {
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Remove .tmp files (incomplete writes)
		if strings.HasSuffix(name, ".tmp") {
			filePath := filepath.Join(s.basePath, name)
			os.Remove(filePath)
		}
	}
}
