package metrics

import (
	"context"
	"errors"
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

func TestTransportEventKindsAreStable(t *testing.T) {
	tests := []struct {
		kind TransportEventKind
		want string
	}{
		{kind: TransportConnectionDelta, want: "connection_delta"},
		{kind: TransportStreamDelta, want: "stream_delta"},
		{kind: TransportQueueRejected, want: "queue_rejected"},
		{kind: TransportHeartbeatPing, want: "heartbeat_ping"},
		{kind: TransportHeartbeatPong, want: "heartbeat_pong"},
		{kind: TransportPeerTimeout, want: "peer_timeout"},
		{kind: TransportRouteUnavailable, want: "route_unavailable"},
	}

	for _, tt := range tests {
		if got := string(tt.kind); got != tt.want {
			t.Errorf("event kind = %q, want %q", got, tt.want)
		}
	}
}

func TestTransportEventCollectorReceivesFields(t *testing.T) {
	wantErr := errors.New("route unavailable")
	want := TransportEvent{
		Transport:    "zmq",
		Kind:         TransportRouteUnavailable,
		Value:        1,
		ConnectionID: "conn-1",
		StreamID:     "stream-1",
		Error:        wantErr,
	}
	c := &transportEventRecorder{}
	c.OnTransportEvent(context.Background(), want)

	if c.event.Transport != want.Transport || c.event.Kind != want.Kind || c.event.Value != want.Value ||
		c.event.ConnectionID != want.ConnectionID || c.event.StreamID != want.StreamID ||
		!errors.Is(c.event.Error, wantErr) {
		t.Fatalf("recorded event = %+v, want %+v", c.event, want)
	}
}

type transportEventRecorder struct {
	event TransportEvent
}

func (c *transportEventRecorder) OnRPCStart(context.Context, RPCInfo) {}

func (c *transportEventRecorder) OnRPCFinish(context.Context, RPCInfo, *status.Status, time.Duration) {
}

func (c *transportEventRecorder) OnStreamEvent(context.Context, StreamEvent) {}

func (c *transportEventRecorder) OnTransportEvent(_ context.Context, event TransportEvent) {
	c.event = event
}
