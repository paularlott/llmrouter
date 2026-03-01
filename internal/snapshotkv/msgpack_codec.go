package snapshotkv

import (
	"github.com/vmihailenco/msgpack/v5"
)

// MsgpackCodec uses github.com/vmihailenco/msgpack/v5 for serialization
type MsgpackCodec struct{}

func (c MsgpackCodec) Encode(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func (c MsgpackCodec) Decode(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}

func (c MsgpackCodec) Extension() string {
	return "msgpack"
}
