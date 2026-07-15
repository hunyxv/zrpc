package server

import (
	"time"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/transport"
)

// Options 描述服务端运行所需的依赖和限制参数。
type Options struct {
	// Transport 是底层传输实现，例如 ZeroMQ transport。
	Transport transport.Transport
	// Endpoint 是服务端监听地址。
	Endpoint transport.Endpoint
	// Codec 负责请求和响应体编解码。
	Codec codec.Codec
	// Metrics 接收服务端侧观测事件；为空时使用 noop collector。
	Metrics metrics.Collector

	// MaxConcurrentStreams 是服务端全局最大并发 stream 数；小于等于 0 表示不限制。
	MaxConcurrentStreams int
	// MaxMessageSize 预留为最大消息大小限制。
	MaxMessageSize int
	// InitialStreamWindow 是新建 stream 的初始窗口大小。
	InitialStreamWindow int
	// MaxConnInFlightBytes 预留为单连接最大在途字节数。
	MaxConnInFlightBytes int
	// GracefulShutdownTimeout 预留为优雅关闭超时时间。
	GracefulShutdownTimeout time.Duration
}
