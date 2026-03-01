# SnapshotKV

A lightweight, snapshot-based storage engine for structured data with optional large blob support.

## Features

- **In-memory operations** with snapshot persistence
- **Debounced saves** with configurable interval (default: 10s)
- **TTL support** with automatic cleanup
- **Blob storage** for large binary data (loaded on demand)
- **Thread-safe** for concurrent access from multiple goroutines
- **Pluggable serialization** (MessagePack default, JSON available)
- **HLC-style naming** for snapshots (65,535 snapshots per second)
- **Atomic writes** with temp file + rename pattern
- **Configurable snapshot history** (rollback capability)

## Installation

```bash
go get github.com/paularlott/snapshotkv
```

## Quick Start

```go
package main

import (
    "fmt"
    "time"

    "github.com/llmrouter/internal/snapshotkv"
)

func main() {
    // Open database
    db, err := snapshotkv.Open("./data", nil)
    if err != nil {
        panic(err)
    }
    defer db.Close()

    // Simple key-value (no expiry)
    db.Set("user:123", map[string]any{
        "name":  "Alice",
        "email": "alice@example.com",
    })

    // With TTL (30 days)
    db.SetEx("session:abc", map[string]any{
        "user_id": "123",
    }, 30*24*time.Hour)

    // Get by key
    user, err := db.Get("user:123")
    if err != nil {
        panic(err)
    }
    fmt.Println("User:", user["name"])

    // Find keys by prefix
    keys := db.FindKeysByPrefix("user:")
    fmt.Println("Users:", keys)

    // Delete
    db.Delete("user:123")
}
```

## Blob Storage

For large data like conversation history, use blob storage:

```go
// Store with blob
db.SetWithBlobEx("conversation:conv-001", map[string]any{
    "status":     "active",
    "created_at": time.Now(),
}, conversationData, 30*24*time.Hour)

// Load blob on demand
blob, err := db.GetBlob("conversation:conv-001")
if err != nil {
    panic(err)
}
fmt.Println("Conversation data:", len(blob), "bytes")

// Update blob
db.UpdateBlob("conversation:conv-001", newConversationData)
```

## Transaction Operations

For bulk writes, use transaction mode to defer saves:

```go
db.BeginTransaction()
for i := 0; i < 1000; i++ {
    key := fmt.Sprintf("item:%d", i)
    db.Set(key, map[string]any{"index": i})
}
db.Commit() // Single save at the end
```

Or rollback without saving:

```go
db.BeginTransaction()
db.Set("temp:key", data)
db.Rollback() // Changes discarded, no save triggered
```

## Configuration

```go
cfg := &snapshotkv.Config{
    Path:               "./data",           // Storage directory
    MaxSnapshots:       10,                 // Snapshot history (default: 10)
    SaveDebounce:       10 * time.Second,   // Debounce interval (default: 10s)
    DisableCompression: false,              // Set true to disable gzip (default: false, compression enabled)
    TTLCleanupInterval: 5 * time.Minute,    // TTL cleanup interval (default: 5m, 0 to disable)
    Codec:              snapshotkv.MsgpackCodec{}, // Serialization (default: MsgpackCodec)
}

db, err := snapshotkv.Open("./data", cfg)
```

## Pluggable Codecs

```go
// MessagePack (default, more compact)
db, _ := snapshotkv.Open("./data", &snapshotkv.Config{
    Codec: snapshotkv.MsgpackCodec{},
})

// JSON (human-readable snapshots)
db, _ := snapshotkv.Open("./data", &snapshotkv.Config{
    Codec:              snapshotkv.JSONCodec{},
    DisableCompression: true, // Disable gzip for readable files
})
```

## Storage Layout

```
data/
  snapshots/
    data-20260301-143022-0001.msgpack.gz
    data-20260301-143022-0000.msgpack.gz
    data-20260301-120000-0000.msgpack.gz
  blobs/
    0ab/
      0abc12345678-1a2b3c4d5e6f.bin
    ff1/
      ff1234567890-2b3c4d5e6f7a.bin
```

### Snapshot Files

- **Format:** MessagePack (or JSON) with optional gzip compression
- **Naming:** `data-YYYYMMDD-HHMMSS-CCCC.<ext>.gz`
- **HLC counter:** 16-bit hex counter for multiple snapshots per second
- **Atomic writes:** Write to temp file, then rename

### Blob Files

- **Naming:** UUID v7 (time-sortable) with `.bin` extension
- **Sharding:** 3 hex chars = 4,096 directories

## API Reference

### Database Operations

```go
// Open/close
db, err := snapshotkv.Open(path string, cfg *Config) (*DB, error)
db.Close() error
db.Save() error  // Force immediate snapshot save

// Transaction mode
db.BeginTransaction() error
db.Commit() error
db.Rollback() error
```

### Key-Value Operations

```go
// Get value by key
value, err := db.Get(key string) (map[string]any, error)

// Set without expiry
db.Set(key string, value map[string]any) error

// Set with TTL
db.SetEx(key string, value map[string]any, ttl time.Duration) error

// Delete key
db.Delete(key string) error

// Find keys by prefix
keys := db.FindKeysByPrefix(prefix string) []string
```

### Blob Operations

```go
// Set with blob (no expiry)
db.SetWithBlob(key string, value map[string]any, blob []byte) error

// Set with blob and TTL
db.SetWithBlobEx(key string, value map[string]any, blob []byte, ttl time.Duration) error

// Get blob
blob, err := db.GetBlob(key string) ([]byte, error)

// Update blob
db.UpdateBlob(key string, blob []byte) error
```

## Error Handling

```go
value, err := db.Get("nonexistent")
if err == snapshotkv.ErrNotFound {
    // Key doesn't exist
}

blob, err := db.GetBlob("key")
if err == snapshotkv.ErrNoBlob {
    // Key exists but has no blob
}
```

## Thread Safety

SnapshotKV is fully safe for concurrent use:

- All operations protected by RWMutex
- Read operations use read lock
- Write operations use write lock
- TTL cleanup runs in background goroutine with proper locking

## When to Use SnapshotKV

**Good fit:**

- Configuration storage
- Session management
- Conversation/message storage
- MCP server configurations
- Small to medium datasets (KB to low MB)
- Workloads with infrequent writes

**Not ideal for:**

- High-throughput write workloads (writes are debounced but still trigger full snapshots)
- Very large datasets (entire dataset loaded into memory)
- Real-time analytics queries

## License

MIT License
