package transport

import (
	"context"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
)

// Endpoint 描述一个 transport 可连接或可监听的地址。
type Endpoint struct {
	// Transport 是传输类型名称，例如 "zmq" 或 "fake"。
	Transport string
	// Address 是传输实现可理解的地址。
	Address string
}

const (
	// MethodMetadataKey 是 request frame 中保存 RPC 方法名的 metadata key。
	MethodMetadataKey = "method"
	// ModeMetadataKey 是 request frame 中保存 RPC 调用模式的 metadata key。
	ModeMetadataKey = "rpc-mode"
	// ModeStream 表示当前调用是流式 RPC。
	ModeStream = "stream"
)

// Transport 定义可插拔传输实现需要提供的能力。
type Transport interface {
	// Dial 建立到 endpoint 的客户端连接。
	Dial(ctx context.Context, endpoint Endpoint, opts DialOptions) (Conn, error)
	// Listen 在 endpoint 上监听服务端连接。
	Listen(endpoint Endpoint, opts ListenOptions) (Listener, error)
	// Name 返回传输实现名称。
	Name() string
}

// Listener 表示服务端监听器。
type Listener interface {
	// Accept 接收一个新连接。
	Accept(ctx context.Context) (Conn, error)
	// Close 关闭监听器。
	Close(ctx context.Context) error
}

// Conn 表示一条可承载多个 stream 的传输连接。
type Conn interface {
	// ID 返回连接本地唯一标识。
	ID() string
	// LocalEndpoint 返回本端 endpoint。
	LocalEndpoint() Endpoint
	// RemoteEndpoint 返回对端 endpoint。
	RemoteEndpoint() Endpoint
	// OpenStream 主动打开一个新的 transport stream。
	OpenStream(ctx context.Context, method string, md metadata.MD) (TransportStream, error)
	// AcceptStream 接收对端打开的 transport stream。
	AcceptStream(ctx context.Context) (TransportStream, error)
	// Close 关闭连接。
	Close(ctx context.Context) error
	// Drain 进入排空状态，拒绝新 stream 但允许已有 stream 继续收尾。
	Drain(ctx context.Context) error
}

// TransportStream 表示传输层 frame 收发通道。
type TransportStream interface {
	// ID 返回 stream id。
	ID() string
	// SendFrame 发送协议 frame。
	SendFrame(ctx context.Context, frame *protocol.Frame) error
	// RecvFrame 接收协议 frame。
	RecvFrame(ctx context.Context) (*protocol.Frame, error)
	// Close 关闭本地 stream。
	Close(ctx context.Context) error
	// Reset 以状态错误重置 stream。
	Reset(ctx context.Context, st *status.Status) error
}
