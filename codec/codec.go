package codec

// Codec encodes and decodes RPC payloads.
type Codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}
