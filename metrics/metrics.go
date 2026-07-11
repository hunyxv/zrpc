package metrics

import (
	"context"
	"time"

	"github.com/hunyxv/zrpc/status"
)

type RPCInfo struct {
	Method string
}

type StreamEvent struct {
	Method string
	Bytes  int
}

type TransportEvent struct {
	Transport string
	Error     error
}

type Collector interface {
	OnRPCStart(ctx context.Context, info RPCInfo)
	OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration)
	OnStreamEvent(ctx context.Context, event StreamEvent)
	OnTransportEvent(ctx context.Context, event TransportEvent)
}

type noopCollector struct{}

func Noop() Collector {
	return noopCollector{}
}

func (noopCollector) OnRPCStart(ctx context.Context, info RPCInfo) {}

func (noopCollector) OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration) {
}

func (noopCollector) OnStreamEvent(ctx context.Context, event StreamEvent) {}

func (noopCollector) OnTransportEvent(ctx context.Context, event TransportEvent) {}
