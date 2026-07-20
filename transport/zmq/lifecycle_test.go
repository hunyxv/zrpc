package zmq

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

func TestConnLifecyclePermissions(t *testing.T) {
	tests := []struct {
		name      string
		local     lifecycleState
		peer      lifecycleState
		canOpen   bool
		canAccept bool
	}{
		{name: "active", local: stateActive, peer: stateActive, canOpen: true, canAccept: true},
		{name: "local draining", local: stateDraining, peer: stateActive},
		{name: "peer draining", local: stateActive, peer: stateDraining},
		{name: "local closing", local: stateClosing, peer: stateActive},
		{name: "peer closing", local: stateActive, peer: stateClosing},
		{name: "local closed", local: stateClosed, peer: stateActive},
		{name: "peer closed", local: stateActive, peer: stateClosed},
		{name: "closed", local: stateClosed, peer: stateClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := connLifecycle{local: tt.local, peer: tt.peer}
			if got := lc.canOpen(); got != tt.canOpen {
				t.Fatalf("canOpen() = %v, want %v", got, tt.canOpen)
			}
			if got := lc.canAccept(); got != tt.canAccept {
				t.Fatalf("canAccept() = %v, want %v", got, tt.canAccept)
			}
		})
	}
}

func TestConnLifecycleTransitions(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*connLifecycle)
		wantLocal  lifecycleState
		wantPeer   lifecycleState
	}{
		{
			name:       "start local drain",
			transition: func(lc *connLifecycle) { lc.startLocalDrain() },
			wantLocal:  stateDraining,
			wantPeer:   stateActive,
		},
		{
			name:       "mark peer draining",
			transition: func(lc *connLifecycle) { lc.markPeerDraining() },
			wantLocal:  stateActive,
			wantPeer:   stateDraining,
		},
		{
			name:       "start local close",
			transition: func(lc *connLifecycle) { lc.startLocalClose() },
			wantLocal:  stateClosing,
			wantPeer:   stateActive,
		},
		{
			name:       "mark peer closing",
			transition: func(lc *connLifecycle) { lc.markPeerClosing() },
			wantLocal:  stateActive,
			wantPeer:   stateClosing,
		},
		{
			name:       "mark closed",
			transition: func(lc *connLifecycle) { lc.markClosed() },
			wantLocal:  stateClosed,
			wantPeer:   stateClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := newConnLifecycle()
			tt.transition(&lc)
			if lc.local != tt.wantLocal || lc.peer != tt.wantPeer {
				t.Fatalf("lifecycle = {%v %v}, want {%v %v}", lc.local, lc.peer, tt.wantLocal, tt.wantPeer)
			}
		})
	}
}

func TestConnLifecycleTransitionsDoNotRegress(t *testing.T) {
	tests := []struct {
		name       string
		initial    connLifecycle
		transition func(*connLifecycle)
	}{
		{
			name:       "local closing ignores drain",
			initial:    connLifecycle{local: stateClosing, peer: stateActive},
			transition: func(lc *connLifecycle) { lc.startLocalDrain() },
		},
		{
			name:       "peer closing ignores drain",
			initial:    connLifecycle{local: stateActive, peer: stateClosing},
			transition: func(lc *connLifecycle) { lc.markPeerDraining() },
		},
		{
			name:       "local closed ignores close",
			initial:    connLifecycle{local: stateClosed, peer: stateActive},
			transition: func(lc *connLifecycle) { lc.startLocalClose() },
		},
		{
			name:       "peer closed ignores close",
			initial:    connLifecycle{local: stateActive, peer: stateClosed},
			transition: func(lc *connLifecycle) { lc.markPeerClosing() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := tt.initial
			tt.transition(&lc)
			if lc != tt.initial {
				t.Fatalf("lifecycle = %+v, want unchanged %+v", lc, tt.initial)
			}
		})
	}
}

func TestConnLocalDrainRejectsNewRequestWithUnavailableReset(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.mu.Lock()
	c.lifecycle.startLocalDrain()
	c.mu.Unlock()

	actions := c.routeFrame(&protocol.Frame{
		Type:      protocol.FrameRequest,
		StreamID:  "stream-1",
		Direction: protocol.DirectionClientToServer,
	})
	if len(actions) != 1 || actions[0].frame == nil {
		t.Fatalf("routeFrame() actions = %#v, want one reset", actions)
	}
	reset := actions[0].frame
	if reset.Type != protocol.FrameReset || reset.Status == nil || reset.Status.Code != status.Unavailable {
		t.Fatalf("reset = %#v, want Unavailable", reset)
	}
}

func TestConnPeerGoAwayRejectsOpenStream(t *testing.T) {
	c := newConn("client-1", transport.Endpoint{}, transport.Endpoint{}, nil, false, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.routeFrame(&protocol.Frame{Type: protocol.FrameGoAway, Seq: 1})
	if _, err := c.OpenStream(context.Background(), "hello.Say", nil); !errors.Is(err, errConnectionDraining) {
		t.Fatalf("OpenStream() error = %v, want %v", err, errConnectionDraining)
	}
}

func TestConnConcurrentCloseSendsOneFrame(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	var sends atomic.Int32
	c.sendFrame = func(_ context.Context, frame *protocol.Frame) error {
		sends.Add(1)
		if frame.Type != protocol.FrameClose {
			t.Fatalf("sent frame type = %v, want close", frame.Type)
		}
		c.routeFrame(&protocol.Frame{Type: protocol.FrameCloseAck, Seq: frame.Seq})
		return nil
	}

	const callers = 8
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- c.Close(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	if got := sends.Load(); got != 1 {
		t.Fatalf("close frame sends = %d, want 1", got)
	}
}

func TestConnCloseHandshakeTimesOutAndClosesLocally(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: 10 * time.Millisecond})
	c.sendFrame = func(context.Context, *protocol.Frame) error { return nil }
	start := time.Now()
	err := c.Close(context.Background())
	if !errors.Is(err, errCloseHandshakeTimeout) {
		t.Fatalf("Close() error = %v, want %v", err, errCloseHandshakeTimeout)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond || elapsed > time.Second {
		t.Fatalf("Close() elapsed = %s", elapsed)
	}
	c.mu.Lock()
	local := c.lifecycle.local
	c.mu.Unlock()
	if local != stateClosed {
		t.Fatalf("local lifecycle = %v, want closed", local)
	}
}

func TestConnPeerCloseKeepsRouteUntilAckCompletes(t *testing.T) {
	l := &listener{conns: make(map[string]*conn), changed: make(chan struct{})}
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, l, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.route = []byte("route-1")
	l.conns[string(c.route)] = c
	actions := c.routeFrame(&protocol.Frame{Type: protocol.FrameClose, Seq: 42})
	if len(actions) != 1 || actions[0].frame == nil {
		t.Fatalf("routeFrame() actions = %#v, want one ack", actions)
	}
	ack := actions[0].frame
	if ack.Type != protocol.FrameCloseAck || ack.Seq != 42 {
		t.Fatalf("ack = %#v, want close ack seq 42", ack)
	}
	l.mu.Lock()
	_, before := l.conns[string(c.route)]
	l.mu.Unlock()
	if !before {
		t.Fatal("route removed before close ack completed")
	}
	actions[0].onComplete(nil)
	l.mu.Lock()
	_, after := l.conns[string(c.route)]
	l.mu.Unlock()
	if after {
		t.Fatal("route retained after close ack completed")
	}
}

func TestConnPeerCloseThenLocalCloseDoesNotSendSecondClose(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	var sends atomic.Int32
	c.sendFrame = func(context.Context, *protocol.Frame) error {
		sends.Add(1)
		return nil
	}
	actions := c.routeFrame(&protocol.Frame{Type: protocol.FrameClose, Seq: 7})
	if len(actions) != 1 {
		t.Fatalf("close actions = %d, want 1", len(actions))
	}
	errCh := make(chan error, 1)
	go func() { errCh <- c.Close(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	if got := sends.Load(); got != 0 {
		t.Fatalf("local Close sent %d additional frames", got)
	}
	actions[0].onComplete(nil)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not share peer-close completion")
	}
}

func TestConnCloseCallerDeadlineBoundsCleanup(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.sendFrame = func(ctx context.Context, _ *protocol.Frame) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		finished := c.closeFinished
		c.mu.Unlock()
		if finished {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("caller deadline did not bound background cleanup")
}

func TestConnPeerDrainingRejectsUnexpectedRequest(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.routeFrame(&protocol.Frame{Type: protocol.FrameGoAway, Seq: 1})
	actions := c.routeFrame(&protocol.Frame{Type: protocol.FrameRequest, StreamID: "stream-1", Direction: protocol.DirectionClientToServer})
	if len(actions) != 1 || actions[0].frame.Status == nil || actions[0].frame.Status.Code != status.Unavailable {
		t.Fatalf("routeFrame() actions = %#v, want Unavailable reset", actions)
	}
}

func TestConnDrainRetriesGoAwayAfterSendFailure(t *testing.T) {
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	var calls atomic.Int32
	wantErr := errors.New("send failed")
	c.sendFrame = func(context.Context, *protocol.Frame) error {
		if calls.Add(1) == 1 {
			return wantErr
		}
		return nil
	}
	if err := c.Drain(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("first Drain() error = %v, want %v", err, wantErr)
	}
	if err := c.Drain(context.Background()); err != nil {
		t.Fatalf("second Drain() error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GoAway sends = %d, want 2", got)
	}
}

func TestListenerRemoveConnDoesNotDeleteReplacement(t *testing.T) {
	l := &listener{conns: make(map[string]*conn), changed: make(chan struct{})}
	old := newConn("old", transport.Endpoint{}, transport.Endpoint{}, l, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	replacement := newConn("new", transport.Endpoint{}, transport.Endpoint{}, l, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	old.route = []byte("same-route")
	replacement.route = []byte("same-route")
	l.conns[string(replacement.route)] = replacement
	l.removeConn(old)
	if got := l.conns[string(replacement.route)]; got != replacement {
		t.Fatalf("replacement = %p, want %p", got, replacement)
	}
}

func TestListenerDoesNotCreateConnForStrayControlFrame(t *testing.T) {
	l := &listener{conns: make(map[string]*conn), changed: make(chan struct{})}
	for _, frame := range []*protocol.Frame{
		{Type: protocol.FramePong, Seq: 1},
		{Type: protocol.FrameCloseAck, Seq: 1},
		{Type: protocol.FrameGoAway, Seq: 1},
	} {
		if actions := l.handleIncoming([]byte("unknown-route"), frame); len(actions) != 0 {
			t.Fatalf("handleIncoming(%v) actions = %#v", frame.Type, actions)
		}
	}
	if len(l.conns) != 0 || len(l.accepts) != 0 {
		t.Fatalf("stray controls created conns=%d accepts=%d", len(l.conns), len(l.accepts))
	}
}
