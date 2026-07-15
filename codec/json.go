package codec

import "encoding/json"

type jsonCodec struct{}

// JSON 返回基于标准库 JSON 的 Codec，主要用于调试和可读性较高的场景。
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
