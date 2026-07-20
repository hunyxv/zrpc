package zmq

import (
	"context"

	"github.com/hunyxv/zrpc/metrics"
)

func emitTransportEvent(collector metrics.Collector, event metrics.TransportEvent) {
	if collector == nil {
		return
	}
	event.Transport = "zmq"
	collector.OnTransportEvent(context.Background(), event)
}
