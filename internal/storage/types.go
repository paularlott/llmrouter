package storage

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Storage-specific types
type StoredResponse struct {
	ID        string                 `json:"id"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Status    ResponseStatus         `json:"status"`
	Request   map[string]interface{} `json:"request"`
	Response  map[string]interface{} `json:"response"`
	Metadata  ResponseMetadata       `json:"metadata"`
}

type ResponseStatus string

const (
	StatusPending    ResponseStatus = "pending"
	StatusInProgress ResponseStatus = "in_progress"
	StatusCompleted  ResponseStatus = "completed"
	StatusCancelled  ResponseStatus = "cancelled"
	StatusError      ResponseStatus = "error"
)

type ResponseMetadata struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ResponseFilter struct {
	Limit  int    `json:"limit,omitempty"`
	Order  string `json:"order,omitempty"`  // "asc" or "desc"
	After  string `json:"after,omitempty"`  // cursor for pagination
	Before string `json:"before,omitempty"` // cursor for pagination
}

type ResponseStorage interface {
	Store(ctx context.Context, response *StoredResponse) error
	Get(ctx context.Context, id string) (*StoredResponse, error)
	List(ctx context.Context, filter ResponseFilter) ([]StoredResponse, error)
	Delete(ctx context.Context, id string) error
	UpdateStatus(ctx context.Context, id string, status ResponseStatus) error
	RunGC() error
	Close() error
}

// Helper function to generate response IDs
func GenerateResponseID() string {
	return "resp_" + strings.ReplaceAll(uuid.New().String(), "-", "")
}