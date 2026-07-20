package metrics

import (
	"context"
	"time"

	"github.com/hunyxv/zrpc/status"
)

// RPCInfo 描述一次 RPC 调用的基础信息。
type RPCInfo struct {
	// Method 是 RPC 方法名。
	Method string
}

// StreamEvent 描述 stream 级观测事件。
type StreamEvent struct {
	// Method 是 RPC 方法名。
	Method string
	// Bytes 是本次事件关联的字节数。
	Bytes int
}

// TransportEventKind 是稳定的 transport 事件类型。
type TransportEventKind string

const (
	TransportConnectionDelta  TransportEventKind = "connection_delta"
	TransportStreamDelta      TransportEventKind = "stream_delta"
	TransportQueueRejected    TransportEventKind = "queue_rejected"
	TransportHeartbeatPing    TransportEventKind = "heartbeat_ping"
	TransportHeartbeatPong    TransportEventKind = "heartbeat_pong"
	TransportPeerTimeout      TransportEventKind = "peer_timeout"
	TransportRouteUnavailable TransportEventKind = "route_unavailable"
)

// TransportEvent 描述 transport 级观测事件。
type TransportEvent struct {
	// Transport 是 transport 名称。
	Transport string
	// Kind 是稳定的事件类型。
	Kind TransportEventKind
	// Value 是事件的增量或计数值。
	Value int64
	// ConnectionID 标识事件关联的连接。
	ConnectionID string
	// StreamID 标识事件关联的 stream。
	StreamID string
	// Error 是可选的 transport 错误。
	Error error
}

// Collector 接收 RPC、stream 和 transport 的观测事件。
type Collector interface {
	// OnRPCStart 在 RPC 开始时调用。
	OnRPCStart(ctx context.Context, info RPCInfo)
	// OnRPCFinish 在 RPC 结束时调用。
	OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration)
	// OnStreamEvent 在 stream 事件发生时调用。
	OnStreamEvent(ctx context.Context, event StreamEvent)
	// OnTransportEvent 在 transport 事件发生时调用。
	OnTransportEvent(ctx context.Context, event TransportEvent)
}

type noopCollector struct{}

// Noop 返回忽略所有事件的 Collector。
func Noop() Collector {
	return noopCollector{}
}

func (noopCollector) OnRPCStart(ctx context.Context, info RPCInfo) {}

func (noopCollector) OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration) {
}

func (noopCollector) OnStreamEvent(ctx context.Context, event StreamEvent) {}

func (noopCollector) OnTransportEvent(ctx context.Context, event TransportEvent) {}
