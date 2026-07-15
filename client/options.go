package client

import (
	"time"

	"github.com/hunyxv/zrpc/balancer"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/resolver"
	"github.com/hunyxv/zrpc/transport"
)

// Options 描述客户端运行所需的依赖和限制参数。
type Options struct {
	// Transport 是底层传输实现，例如 ZeroMQ transport。
	Transport transport.Transport
	// Target 是客户端默认连接目标。
	Target transport.Endpoint
	// Resolver 将目标解析为 endpoint 列表；为空时使用 StaticResolver。
	Resolver resolver.Resolver
	// Balancer 从 resolver 返回的 endpoint 列表中选择目标；为空时使用 PickFirst。
	Balancer balancer.Balancer
	// Codec 负责请求和响应体编解码。
	Codec codec.Codec
	// Metrics 接收客户端侧观测事件；为空时使用 noop collector。
	Metrics metrics.Collector

	// DefaultTimeout 预留为默认调用超时；当前调用仍以传入 context 为准。
	DefaultTimeout time.Duration
	// MaxMessageSize 预留为最大消息大小限制。
	MaxMessageSize int
	// InitialStreamWindow 是新建 stream 的初始窗口大小。
	InitialStreamWindow int
	// MaxConnInFlightBytes 预留为单连接最大在途字节数。
	MaxConnInFlightBytes int
}
