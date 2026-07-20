package zmq

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

func TestZMQMetricsDefaultsToNoop(t *testing.T) {
	opts := defaultOptions(Options{})
	if opts.Metrics == nil {
		t.Fatal("defaultOptions().Metrics = nil, want noop collector")
	}
	emitTransportEvent(opts.Metrics, metrics.TransportEvent{Kind: metrics.TransportConnectionDelta, Value: 1})
}

func TestZMQMetricsEmitsTransportEvent(t *testing.T) {
	wantErr := errors.New("queue full")
	recorder := &zmqMetricsRecorder{}
	opts := defaultOptions(Options{Metrics: recorder})
	if opts.Metrics != recorder {
		t.Fatal("defaultOptions() replaced configured metrics collector")
	}

	emitTransportEvent(opts.Metrics, metrics.TransportEvent{
		Kind:         metrics.TransportQueueRejected,
		Value:        1,
		ConnectionID: "conn-1",
		StreamID:     "stream-1",
		Error:        wantErr,
	})

	got := recorder.lastEvent()
	if got.Transport != "zmq" || got.Kind != metrics.TransportQueueRejected || got.Value != 1 ||
		got.ConnectionID != "conn-1" || got.StreamID != "stream-1" || !errors.Is(got.Error, wantErr) {
		t.Fatalf("event = %+v, want complete zmq queue rejection event", got)
	}
}

func TestZMQMetricsNilCollectorIsSafe(t *testing.T) {
	emitTransportEvent(nil, metrics.TransportEvent{Kind: metrics.TransportConnectionDelta, Value: 1})
}

func TestZMQMetricsConnectionAndStreamDeltasExactlyOnce(t *testing.T) {
	recorder := &zmqMetricsRecorder{}
	c := newConn("conn-1", transport.Endpoint{}, transport.Endpoint{}, nil, false, Options{
		Metrics:               recorder,
		RecvQueueSize:         1,
		CloseHandshakeTimeout: time.Second,
	})
	recorder.setHook(func(event metrics.TransportEvent) {
		if event.Kind != metrics.TransportConnectionDelta && event.Kind != metrics.TransportStreamDelta {
			return
		}
		if !c.mu.TryLock() {
			t.Error("collector invoked while conn mutex was held")
			return
		}
		c.mu.Unlock()
	})

	first := newStream("stream-1", c)
	if err := c.addStream(first); err != nil {
		t.Fatalf("addStream(first) error = %v", err)
	}
	c.removeStream(first.id)
	c.removeStream(first.id)

	second := newStream("stream-2", c)
	if err := c.addStream(second); err != nil {
		t.Fatalf("addStream(second) error = %v", err)
	}
	c.finishClose(nil)
	c.finishClose(nil)

	assertMetricValues(t, recorder.eventsOfKind(metrics.TransportConnectionDelta), []int64{1, -1})
	assertMetricValues(t, recorder.eventsOfKind(metrics.TransportStreamDelta), []int64{1, -1, 1, -1})
}

func TestZMQMetricsHeartbeatEvents(t *testing.T) {
	recorder := &zmqMetricsRecorder{}
	start := time.Unix(100, 0)
	c := newConn("conn-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{
		Metrics:           recorder,
		RecvQueueSize:     1,
		HeartbeatInterval: 10 * time.Second,
		PeerTimeout:       30 * time.Second,
	})
	c.mu.Lock()
	c.heartbeat = newHeartbeatState(start)
	c.mu.Unlock()

	actions := c.heartbeatActions(start.Add(10 * time.Second))
	if len(actions) != 1 || actions[0].frame.Type != protocol.FramePing {
		t.Fatalf("heartbeat actions = %#v, want ping", actions)
	}
	actions = c.routeFrame(&protocol.Frame{Type: protocol.FramePing, Seq: 9})
	if len(actions) != 1 || actions[0].frame.Type != protocol.FramePong {
		t.Fatalf("ping actions = %#v, want pong", actions)
	}

	if got := recorder.eventsOfKind(metrics.TransportHeartbeatPing); len(got) != 1 || got[0].ConnectionID != "conn-1" {
		t.Fatalf("heartbeat ping events = %+v, want one for conn-1", got)
	}
	if got := recorder.eventsOfKind(metrics.TransportHeartbeatPong); len(got) != 1 || got[0].ConnectionID != "conn-1" {
		t.Fatalf("heartbeat pong events = %+v, want one for conn-1", got)
	}
}

func TestZMQMetricsPeerTimeoutOnce(t *testing.T) {
	recorder := &zmqMetricsRecorder{}
	start := time.Unix(100, 0)
	c := newConn("conn-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{
		Metrics:           recorder,
		RecvQueueSize:     1,
		HeartbeatInterval: 10 * time.Second,
		PeerTimeout:       30 * time.Second,
	})
	c.mu.Lock()
	c.heartbeat = newHeartbeatState(start)
	c.mu.Unlock()

	c.heartbeatActions(start.Add(30 * time.Second))
	c.heartbeatActions(start.Add(31 * time.Second))
	events := recorder.eventsOfKind(metrics.TransportPeerTimeout)
	if len(events) != 1 || events[0].ConnectionID != "conn-1" || status.FromError(events[0].Error).Code != status.Unavailable {
		t.Fatalf("peer timeout events = %+v, want one Unavailable event for conn-1", events)
	}
}

func TestZMQMetricsOwnerQueueRejected(t *testing.T) {
	recorder := &zmqMetricsRecorder{}
	o := newQueueTestOwner(t, 1, 64, 1)
	o.metrics = recorder
	held, ok := o.controlBudget.tryAcquire(1)
	if !ok {
		t.Fatal("control budget reservation failed")
	}
	defer held.release()

	if o.enqueueAction(routeFrameAction{
		connectionID: "conn-1",
		frame:        &protocol.Frame{Type: protocol.FramePong, Seq: 1},
	}) {
		t.Fatal("enqueueAction() = true with exhausted control budget")
	}
	events := recorder.eventsOfKind(metrics.TransportQueueRejected)
	if len(events) != 1 || events[0].ConnectionID != "conn-1" || events[0].Value != 1 ||
		status.FromError(events[0].Error).Code != status.ResourceExhausted {
		t.Fatalf("queue rejected events = %+v, want one ResourceExhausted event", events)
	}
}

func TestZMQMetricsOwnerRouteUnavailable(t *testing.T) {
	recorder := &zmqMetricsRecorder{}
	o := newQueueTestOwner(t, 1, 64, 1)
	o.metrics = recorder
	reservation, err := o.dataBudget.acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("data budget acquire error = %v", err)
	}
	req := &sendRequest{
		ctx:          context.Background(),
		route:        []byte("route-1"),
		raw:          []byte("data"),
		reservation:  reservation,
		connectionID: "conn-1",
		streamID:     "stream-1",
	}
	o.sendRaw = func([]byte, []byte) error { return zmq4.Errno(syscall.EHOSTUNREACH) }
	if !o.trySend(req) {
		t.Fatal("trySend() did not complete route unavailable request")
	}

	events := recorder.eventsOfKind(metrics.TransportRouteUnavailable)
	if len(events) != 1 || events[0].ConnectionID != "conn-1" || events[0].StreamID != "stream-1" ||
		zmq4.AsErrno(events[0].Error) != zmq4.Errno(syscall.EHOSTUNREACH) {
		t.Fatalf("route unavailable events = %+v, want one complete event", events)
	}
}

func assertMetricValues(t *testing.T, events []metrics.TransportEvent, want []int64) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for i := range events {
		if events[i].Value != want[i] {
			t.Fatalf("event[%d].Value = %d, want %d", i, events[i].Value, want[i])
		}
	}
}

type zmqMetricsRecorder struct {
	mu     sync.Mutex
	events []metrics.TransportEvent
	hook   func(metrics.TransportEvent)
}

func (c *zmqMetricsRecorder) OnRPCStart(context.Context, metrics.RPCInfo) {}

func (c *zmqMetricsRecorder) OnRPCFinish(context.Context, metrics.RPCInfo, *status.Status, time.Duration) {
}

func (c *zmqMetricsRecorder) OnStreamEvent(context.Context, metrics.StreamEvent) {}

func (c *zmqMetricsRecorder) OnTransportEvent(_ context.Context, event metrics.TransportEvent) {
	c.mu.Lock()
	c.events = append(c.events, event)
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		hook(event)
	}
}

func (c *zmqMetricsRecorder) lastEvent() metrics.TransportEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[len(c.events)-1]
}

func (c *zmqMetricsRecorder) eventsOfKind(kind metrics.TransportEventKind) []metrics.TransportEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var events []metrics.TransportEvent
	for _, event := range c.events {
		if event.Kind == kind {
			events = append(events, event)
		}
	}
	return events
}

func (c *zmqMetricsRecorder) setHook(hook func(metrics.TransportEvent)) {
	c.mu.Lock()
	c.hook = hook
	c.mu.Unlock()
}
