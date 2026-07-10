package codec

import "github.com/vmihailenco/msgpack/v5"

type msgpackCodec struct{}

// Msgpack returns a codec backed by github.com/vmihailenco/msgpack/v5.
func Msgpack() Codec {
	return msgpackCodec{}
}

func (msgpackCodec) Name() string {
	return "msgpack"
}

func (msgpackCodec) Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func (msgpackCodec) Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}
