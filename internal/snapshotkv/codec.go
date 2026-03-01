package snapshotkv

// Codec interface for pluggable serialization
type Codec interface {
	Encode(v any) ([]byte, error)
	Decode(data []byte, v any) error
	Extension() string // File extension without dot, e.g., "msgpack" or "json"
}
