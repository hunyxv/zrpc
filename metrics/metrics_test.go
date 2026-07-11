package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/status"
)

func TestNoopCollector(t *testing.T) {
	c := Noop()
	c.OnRPCStart(context.Background(), RPCInfo{Method: "hello.Say"})
	c.OnRPCFinish(context.Background(), RPCInfo{Method: "hello.Say"}, &status.Status{Code: status.OK}, time.Millisecond)
	c.OnStreamEvent(context.Background(), StreamEvent{Method: "hello.Stream", Bytes: 42})
	c.OnTransportEvent(context.Background(), TransportEvent{Transport: "fake"})
}
