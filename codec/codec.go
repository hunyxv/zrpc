package codec

// Codec 定义 RPC 负载的编码和解码接口。
type Codec interface {
	// Name 返回编解码器名称。
	Name() string
	// Marshal 将业务值编码为字节序列。
	Marshal(v any) ([]byte, error)
	// Unmarshal 将字节序列解码到业务值。
	Unmarshal(data []byte, v any) error
}
