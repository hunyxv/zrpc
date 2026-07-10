package codec

import "encoding/json"

type jsonCodec struct{}

// JSON returns a codec backed by the standard JSON encoder.
func JSON() Codec {
	return jsonCodec{}
}

func (jsonCodec) Name() string {
	return "json"
}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
