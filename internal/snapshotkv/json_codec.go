package snapshotkv

import (
	"encoding/json"
)

// JSONCodec uses encoding/json for serialization
type JSONCodec struct{}

func (c JSONCodec) Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (c JSONCodec) Decode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (c JSONCodec) Extension() string {
	return "json"
}
