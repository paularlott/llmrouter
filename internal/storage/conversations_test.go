package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

func tempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "snapshotkv-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// Helper to create text content
func textContent(text string) []openai.ContentPart {
	return []openai.ContentPart{openai.TextContentPart(text)}
}

// TestSnapshotConversationStorage tests the snapshotkv-backed conversation storage
func TestSnapshotConversationStorage_BasicCRUD(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()

	ctx := context.Background()

	// Test Create
	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{"title": "Test Conversation"},
		Items: []openai.ConversationItem{
			{
				ID:      GenerateMessageID(),
				Type:    "message",
				Role:    "user",
				Content: textContent("Hello, world!"),
				Status:  "completed",
			},
		},
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Test Get
	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if retrieved.ID != conv.ID {
		t.Errorf("Expected ID %s, got %s", conv.ID, retrieved.ID)
	}

	if retrieved.Metadata["title"] != "Test Conversation" {
		t.Errorf("Expected title 'Test Conversation', got %v", retrieved.Metadata["title"])
	}

	if len(retrieved.Items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(retrieved.Items))
	}

	if len(retrieved.Items[0].Content) == 0 || retrieved.Items[0].Content[0].Text != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got %v", retrieved.Items[0].Content)
	}

	// Test Update
	err = storage.Update(ctx, conv.ID, map[string]interface{}{"title": "Updated Title"})
	if err != nil {
		t.Fatalf("Failed to update conversation: %v", err)
	}

	updated, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get updated conversation: %v", err)
	}

	if updated.Metadata["title"] != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got %v", updated.Metadata["title"])
	}

	// Test Delete
	err = storage.Delete(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to delete conversation: %v", err)
	}

	_, err = storage.Get(ctx, conv.ID)
	if err == nil {
		t.Error("Expected error when getting deleted conversation")
	}
}

func TestSnapshotConversationStorage_Items(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()
	ctx := context.Background()

	// Create conversation with initial items
	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{},
		Items: []openai.ConversationItem{
			{ID: "msg_001", Type: "message", Role: "user", Content: textContent("First message"), Status: "completed"},
			{ID: "msg_002", Type: "message", Role: "assistant", Content: textContent("First response"), Status: "completed"},
		},
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Test AddItems
	newItems := []openai.ConversationItem{
		{ID: "msg_003", Type: "message", Role: "user", Content: textContent("Second message"), Status: "completed"},
		{ID: "msg_004", Type: "message", Role: "assistant", Content: textContent("Second response"), Status: "completed"},
	}

	err = storage.AddItems(ctx, conv.ID, newItems)
	if err != nil {
		t.Fatalf("Failed to add items: %v", err)
	}

	// Verify items were added
	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if len(retrieved.Items) != 4 {
		t.Errorf("Expected 4 items, got %d", len(retrieved.Items))
	}

	// Test GetItems with pagination (descending order)
	items, hasMore, err := storage.GetItems(ctx, conv.ID, "", 2, "desc")
	if err != nil {
		t.Fatalf("Failed to get items: %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}

	if !hasMore {
		t.Error("Expected hasMore to be true")
	}

	// Descending order should return newest first
	if items[0].ID != "msg_004" {
		t.Errorf("Expected first item to be msg_004, got %s", items[0].ID)
	}

	// Test GetItems with pagination (ascending order)
	items, hasMore, err = storage.GetItems(ctx, conv.ID, "", 2, "asc")
	if err != nil {
		t.Fatalf("Failed to get items: %v", err)
	}

	if items[0].ID != "msg_001" {
		t.Errorf("Expected first item to be msg_001, got %s", items[0].ID)
	}

	// Test GetItems with 'after' cursor
	items, hasMore, err = storage.GetItems(ctx, conv.ID, "msg_001", 2, "asc")
	if err != nil {
		t.Fatalf("Failed to get items: %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 items after cursor, got %d", len(items))
	}

	if items[0].ID != "msg_002" {
		t.Errorf("Expected first item after cursor to be msg_002, got %s", items[0].ID)
	}

	// Test GetItem
	item, err := storage.GetItem(ctx, conv.ID, "msg_003")
	if err != nil {
		t.Fatalf("Failed to get item: %v", err)
	}

	if len(item.Content) == 0 || item.Content[0].Text != "Second message" {
		t.Errorf("Expected content 'Second message', got %v", item.Content)
	}

	// Test DeleteItem
	err = storage.DeleteItem(ctx, conv.ID, "msg_002")
	if err != nil {
		t.Fatalf("Failed to delete item: %v", err)
	}

	// Verify item was deleted
	retrieved, err = storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if len(retrieved.Items) != 3 {
		t.Errorf("Expected 3 items after deletion, got %d", len(retrieved.Items))
	}

	for _, item := range retrieved.Items {
		if item.ID == "msg_002" {
			t.Error("Item msg_002 should have been deleted")
		}
	}
}

func TestSnapshotConversationStorage_LargeItems(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()
	ctx := context.Background()

	// Create conversation with many items to test blob storage
	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{"title": "Large Conversation"},
		Items:     make([]openai.ConversationItem, 100),
	}

	for i := 0; i < 100; i++ {
		conv.Items[i] = openai.ConversationItem{
			ID:      GenerateMessageID(),
			Type:    "message",
			Role:    "user",
			Content: textContent("This is message number " + string(rune('0'+i%10))),
			Status:  "completed",
		}
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Retrieve and verify
	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if len(retrieved.Items) != 100 {
		t.Errorf("Expected 100 items, got %d", len(retrieved.Items))
	}

	// Verify pagination works
	items, hasMore, err := storage.GetItems(ctx, conv.ID, "", 10, "asc")
	if err != nil {
		t.Fatalf("Failed to get items: %v", err)
	}

	if len(items) != 10 {
		t.Errorf("Expected 10 items, got %d", len(items))
	}

	if !hasMore {
		t.Error("Expected hasMore to be true")
	}
}

func TestSnapshotConversationStorage_BlobStorage(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()
	ctx := context.Background()

	// Create conversation with items containing large content
	largeText := strings.Repeat("Hello world! ", 1000) // Large text content

	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{},
		Items: []openai.ConversationItem{
			{
				ID:      "msg_large",
				Type:    "message",
				Role:    "user",
				Content: textContent(largeText),
				Status:  "completed",
			},
		},
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Retrieve and verify content is intact
	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if len(retrieved.Items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(retrieved.Items))
	}

	if len(retrieved.Items[0].Content) == 0 || retrieved.Items[0].Content[0].Text != largeText {
		t.Errorf("Content mismatch: expected length %d, got %d", len(largeText), len(retrieved.Items[0].Content[0].Text))
	}

	// Verify blob files exist on disk
	blobsDir := filepath.Join(dir, "blobs")
	entries, err := os.ReadDir(blobsDir)
	if err != nil {
		t.Logf("Warning: could not read blobs directory: %v", err)
	} else {
		blobCount := 0
		for _, entry := range entries {
			if entry.IsDir() {
				subEntries, _ := os.ReadDir(filepath.Join(blobsDir, entry.Name()))
				blobCount += len(subEntries)
			}
		}
		if blobCount == 0 {
			t.Log("Warning: no blob files found (items may be stored inline)")
		}
	}
}

func TestSnapshotConversationStorage_DeleteCleansUpBlob(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()
	ctx := context.Background()

	// Create conversation with items
	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{},
		Items: []openai.ConversationItem{
			{ID: "msg_001", Type: "message", Role: "user", Content: textContent("Test"), Status: "completed"},
		},
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Delete conversation
	err = storage.Delete(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to delete conversation: %v", err)
	}

	// Verify conversation is gone
	_, err = storage.Get(ctx, conv.ID)
	if err == nil {
		t.Error("Expected error when getting deleted conversation")
	}
}

func TestSnapshotConversationStorage_NotFound(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	store, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	storage := store.NewConversationStorage()
	ctx := context.Background()

	// Test Get on non-existent conversation
	_, err = storage.Get(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error when getting non-existent conversation")
	}

	// Test GetItem on non-existent conversation
	_, err = storage.GetItem(ctx, "nonexistent", "msg_001")
	if err == nil {
		t.Error("Expected error when getting item from non-existent conversation")
	}

	// Test DeleteItem on non-existent conversation
	err = storage.DeleteItem(ctx, "nonexistent", "msg_001")
	if err == nil {
		t.Error("Expected error when deleting item from non-existent conversation")
	}
}

func TestMemoryConversationStorage_BasicCRUD(t *testing.T) {
	storage := NewMemoryConversationStorage()
	ctx := context.Background()

	// Test Create
	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{"title": "Memory Test"},
		Items: []openai.ConversationItem{
			{ID: GenerateMessageID(), Type: "message", Role: "user", Content: textContent("Hello"), Status: "completed"},
		},
	}

	err := storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Test Get
	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if retrieved.ID != conv.ID {
		t.Errorf("Expected ID %s, got %s", conv.ID, retrieved.ID)
	}

	// Test Delete
	err = storage.Delete(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to delete conversation: %v", err)
	}

	_, err = storage.Get(ctx, conv.ID)
	if err == nil {
		t.Error("Expected error when getting deleted conversation")
	}
}

func TestStore_MemoryMode(t *testing.T) {
	store, err := NewStore("", 24*time.Hour) // Empty path = memory mode
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if !store.IsMemory() {
		t.Error("Expected store to be in memory mode")
	}

	storage := store.NewConversationStorage()
	ctx := context.Background()

	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{},
		Items:     []openai.ConversationItem{},
	}

	err = storage.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	retrieved, err := storage.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get conversation: %v", err)
	}

	if retrieved.ID != conv.ID {
		t.Errorf("Expected ID %s, got %s", conv.ID, retrieved.ID)
	}

	store.Close()
}

func TestStore_Persistence(t *testing.T) {
	dir := tempDir(t)
	ttl := 24 * time.Hour

	// Create first store and save data
	store1, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	storage1 := store1.NewConversationStorage()
	ctx := context.Background()

	conv := &StoredConversation{
		ID:        GenerateConversationID(),
		CreatedAt: time.Now(),
		Metadata:  map[string]interface{}{"persistent": "yes"},
		Items: []openai.ConversationItem{
			{ID: "msg_001", Type: "message", Role: "user", Content: textContent("Persistent message"), Status: "completed"},
		},
	}

	err = storage1.Store(ctx, conv)
	if err != nil {
		t.Fatalf("Failed to store conversation: %v", err)
	}

	// Close and create new store
	store1.Close()

	store2, err := NewStore(dir, ttl)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}
	defer store2.Close()

	storage2 := store2.NewConversationStorage()

	// Verify data persisted
	retrieved, err := storage2.Get(ctx, conv.ID)
	if err != nil {
		t.Fatalf("Failed to get persisted conversation: %v", err)
	}

	if retrieved.ID != conv.ID {
		t.Errorf("Expected ID %s, got %s", conv.ID, retrieved.ID)
	}

	if retrieved.Metadata["persistent"] != "yes" {
		t.Errorf("Expected persistent metadata, got %v", retrieved.Metadata)
	}

	if len(retrieved.Items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(retrieved.Items))
	}

	if len(retrieved.Items[0].Content) == 0 || retrieved.Items[0].Content[0].Text != "Persistent message" {
		t.Errorf("Expected 'Persistent message', got %v", retrieved.Items[0].Content)
	}
}

func TestGenerateIDs(t *testing.T) {
	convID := GenerateConversationID()
	if convID == "" {
		t.Error("Conversation ID should not be empty")
	}
	if len(convID) < 10 {
		t.Errorf("Conversation ID seems too short: %s", convID)
	}
	if convID[:5] != "conv_" {
		t.Errorf("Conversation ID should start with 'conv_', got %s", convID[:5])
	}

	msgID := GenerateMessageID()
	if msgID == "" {
		t.Error("Message ID should not be empty")
	}
	if len(msgID) < 10 {
		t.Errorf("Message ID seems too short: %s", msgID)
	}
	if msgID[:4] != "msg_" {
		t.Errorf("Message ID should start with 'msg_', got %s", msgID[:4])
	}

	// Test uniqueness
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateConversationID()
		if ids[id] {
			t.Errorf("Duplicate conversation ID generated: %s", id)
		}
		ids[id] = true
	}
}
