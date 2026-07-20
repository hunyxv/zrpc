package zmq

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

func TestOwnerClassifiesControlFrames(t *testing.T) {
	for _, tt := range []struct {
		name      string
		frameType protocol.FrameType
		want      bool
	}{
		{name: "window update", frameType: protocol.FrameWindowUpdate, want: true},
		{name: "reset", frameType: protocol.FrameReset, want: true},
		{name: "go away", frameType: protocol.FrameGoAway, want: true},
		{name: "close", frameType: protocol.FrameClose, want: true},
		{name: "close ack", frameType: protocol.FrameCloseAck, want: true},
		{name: "ping", frameType: protocol.FramePing, want: true},
		{name: "pong", frameType: protocol.FramePong, want: true},
		{name: "request", frameType: protocol.FrameRequest, want: false},
		{name: "data", frameType: protocol.FrameData, want: false},
		{name: "response", frameType: protocol.FrameResponse, want: false},
		{name: "end", frameType: protocol.FrameEnd, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isControlFrame(tt.frameType); got != tt.want {
				t.Fatalf("isControlFrame(%v) = %v, want %v", tt.frameType, got, tt.want)
			}
		})
	}
}

func TestOwnerRequestKeepsBudgetUntilComplete(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	reservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	req := &sendRequest{reservation: reservation, result: make(chan error, 1)}
	o.dataCh <- req
	head := <-o.dataCh

	if _, ok := o.dataBudget.tryAcquire(1); ok {
		t.Fatal("dequeued head released its admission budget")
	}
	head.complete(nil)
	if next, ok := o.dataBudget.tryAcquire(16); !ok {
		t.Fatal("completed head did not release its budget")
	} else {
		next.release()
	}
}

func TestOwnerControlBudgetIsIndependent(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	data, err := o.dataBudget.acquire(context.Background(), 16)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	control, ok := o.controlBudget.tryAcquire(1)
	if !ok {
		t.Fatal("control budget blocked by full data budget")
	}
	data.release()
	control.release()
}

func TestSendRequestCompleteIsIdempotent(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	reservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	completed := 0
	req := &sendRequest{
		reservation: reservation,
		result:      make(chan error, 1),
		onComplete: func(error) {
			completed++
		},
	}
	req.complete(nil)
	req.complete(errors.New("duplicate"))
	if completed != 1 {
		t.Fatalf("onComplete calls = %d, want 1", completed)
	}
	if next, ok := o.dataBudget.tryAcquire(16); !ok {
		t.Fatal("complete did not release reservation")
	} else {
		next.release()
	}
	select {
	case err := <-req.result:
		if err != nil {
			t.Fatalf("first completion error = %v", err)
		}
	default:
		t.Fatal("completion result not delivered")
	}
}

func TestOwnerInternalActionReportsFullControlQueue(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	first := routeFrameAction{
		frame: &protocol.Frame{
			Type:     protocol.FrameReset,
			StreamID: "stream-1",
			Status:   &status.Status{Code: status.ResourceExhausted},
		},
	}
	if !o.enqueueAction(first) {
		t.Fatal("first enqueueAction() = false, want true")
	}

	var gotErr error
	second := first
	second.onComplete = func(err error) { gotErr = err }
	if o.enqueueAction(second) {
		t.Fatal("second enqueueAction() = true with full control queue")
	}
	if gotErr == nil {
		t.Fatal("full control queue did not report an error")
	}

	queued := <-o.controlCh
	queued.complete(nil)
}

func TestOwnerFailAllReleasesQueuedBudgets(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	dataReservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	controlReservation, ok := o.controlBudget.tryAcquire(1)
	if !ok {
		t.Fatal("control tryAcquire() = false")
	}
	dataReq := &sendRequest{reservation: dataReservation, result: make(chan error, 1)}
	controlReq := &sendRequest{reservation: controlReservation, result: make(chan error, 1)}
	o.dataCh <- dataReq
	o.controlCh <- controlReq
	wantErr := errors.New("owner failed")
	o.failAll(nil, nil, wantErr)

	for _, req := range []*sendRequest{dataReq, controlReq} {
		select {
		case err := <-req.result:
			if !errors.Is(err, wantErr) {
				t.Fatalf("completion error = %v, want %v", err, wantErr)
			}
		default:
			t.Fatal("queued request was not completed")
		}
	}
	if next, ok := o.dataBudget.tryAcquire(16); !ok {
		t.Fatal("data budget leaked after failAll")
	} else {
		next.release()
	}
	if next, ok := o.controlBudget.tryAcquire(1); !ok {
		t.Fatal("control budget leaked after failAll")
	} else {
		next.release()
	}
}

func TestOwnerEAGAINKeepsHeadAndBudget(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	reservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	req := &sendRequest{
		ctx:         context.Background(),
		raw:         []byte("request"),
		result:      make(chan error, 1),
		reservation: reservation,
	}
	o.sendRaw = func([]byte, []byte) error { return zmq4.Errno(syscall.EAGAIN) }
	if o.trySend(req) {
		t.Fatal("trySend() completed an EAGAIN request")
	}
	if _, ok := o.dataBudget.tryAcquire(1); ok {
		t.Fatal("EAGAIN head released its admission budget")
	}

	o.sendRaw = func([]byte, []byte) error { return nil }
	if !o.trySend(req) {
		t.Fatal("trySend() did not complete a successful retry")
	}
	if next, ok := o.dataBudget.tryAcquire(16); !ok {
		t.Fatal("successful retry did not release its admission budget")
	} else {
		next.release()
	}
}

func TestOwnerRejectedEnqueueCanReleaseBudget(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	o.beginStop()
	reservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	req := &sendRequest{reservation: reservation, result: make(chan error, 1)}
	if err := o.enqueue(o.dataCh, req); !errors.Is(err, errOwnerClosed) {
		t.Fatalf("enqueue() error = %v, want %v", err, errOwnerClosed)
	} else {
		req.complete(err)
	}
	if next, ok := o.dataBudget.tryAcquire(16); !ok {
		t.Fatal("rejected enqueue leaked its admission budget")
	} else {
		next.release()
	}
}

func TestOwnerFlushQueuesLimitsControlBurst(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, ownerControlBurst+1)
	for i := 0; i < ownerControlBurst+1; i++ {
		reservation, ok := o.controlBudget.tryAcquire(1)
		if !ok {
			t.Fatalf("control reservation %d failed", i)
		}
		o.controlCh <- &sendRequest{
			ctx:         context.Background(),
			raw:         []byte("control"),
			reservation: reservation,
		}
	}
	dataReservation, err := o.dataBudget.acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	o.dataCh <- &sendRequest{
		ctx:         context.Background(),
		raw:         []byte("data"),
		reservation: dataReservation,
	}
	var sent []string
	o.sendRaw = func(_ []byte, raw []byte) error {
		sent = append(sent, string(raw))
		return nil
	}
	var dataHead, controlHead *sendRequest
	o.flushQueues(&dataHead, &controlHead)

	if len(sent) != ownerControlBurst+1 {
		t.Fatalf("sent count = %d, want %d", len(sent), ownerControlBurst+1)
	}
	for i := 0; i < ownerControlBurst; i++ {
		if sent[i] != "control" {
			t.Fatalf("sent[%d] = %q, want control", i, sent[i])
		}
	}
	if sent[ownerControlBurst] != "data" {
		t.Fatalf("sent[%d] = %q, want data", ownerControlBurst, sent[ownerControlBurst])
	}
	if got := len(o.controlCh); got != 1 {
		t.Fatalf("remaining control requests = %d, want 1", got)
	}
}

func TestOwnerEAGAINHeadBlocksDataButNotControlBudget(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	reservation, err := o.dataBudget.acquire(context.Background(), 8)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	o.dataCh <- &sendRequest{
		ctx:         context.Background(),
		raw:         []byte("request"),
		reservation: reservation,
	}
	o.sendRaw = func([]byte, []byte) error { return zmq4.Errno(syscall.EAGAIN) }
	var dataHead, controlHead *sendRequest
	o.flushQueues(&dataHead, &controlHead)
	if dataHead == nil {
		t.Fatal("EAGAIN data head was discarded")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := o.dataBudget.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second data acquire() error = %v, want deadline exceeded", err)
	}
	if control, ok := o.controlBudget.tryAcquire(1); !ok {
		t.Fatal("control budget blocked by EAGAIN data head")
	} else {
		control.release()
	}
	dataHead.complete(errOwnerClosed)
}

func TestOwnerCloseBarrierCancelsRouteDataBeforeClose(t *testing.T) {
	o := newQueueTestOwner(t, 2, 32, 1)
	dataReservation, err := o.dataBudget.acquire(context.Background(), 4)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	dataReq := &sendRequest{
		ctx:         context.Background(),
		route:       []byte("route-1"),
		raw:         []byte("data"),
		result:      make(chan error, 1),
		reservation: dataReservation,
	}
	if err := o.enqueueRequest(dataReq, false, false); err != nil {
		t.Fatalf("enqueue data: %v", err)
	}
	controlReservation, ok := o.controlBudget.tryAcquire(5)
	if !ok {
		t.Fatal("control acquire failed")
	}
	closeReq := &sendRequest{
		ctx:         context.Background(),
		route:       []byte("route-1"),
		raw:         []byte("close"),
		reservation: controlReservation,
	}
	if err := o.enqueueRequest(closeReq, true, true); err != nil {
		t.Fatalf("enqueue close: %v", err)
	}
	var sent []string
	o.sendRaw = func(_ []byte, raw []byte) error {
		sent = append(sent, string(raw))
		return nil
	}
	var dataHead, controlHead *sendRequest
	o.flushQueues(&dataHead, &controlHead)
	if len(sent) != 1 || sent[0] != "close" {
		t.Fatalf("sent = %v, want only close", sent)
	}
	select {
	case err := <-dataReq.result:
		if !errors.Is(err, errConnectionClosed) {
			t.Fatalf("data completion error = %v, want connection closed", err)
		}
	default:
		t.Fatal("barrier did not complete queued data")
	}

	nextReservation, err := o.dataBudget.acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("next data acquire() error = %v", err)
	}
	next := &sendRequest{ctx: context.Background(), route: []byte("route-1"), raw: []byte("x"), reservation: nextReservation}
	if err := o.enqueueRequest(next, false, false); !errors.Is(err, errConnectionClosed) {
		t.Fatalf("data after barrier error = %v, want connection closed", err)
	}
	next.complete(errConnectionClosed)
}

func TestConnCloseInstallsBarrierBeforeControlBudgetAvailable(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	heldControl, ok := o.controlBudget.tryAcquire(1)
	if !ok {
		t.Fatal("control reservation failed")
	}
	defer heldControl.release()
	dataReservation, err := o.dataBudget.acquire(context.Background(), 4)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	dataReq := &sendRequest{
		ctx:         context.Background(),
		route:       []byte("route-1"),
		raw:         []byte("data"),
		result:      make(chan error, 1),
		reservation: dataReservation,
	}
	if err := o.enqueueRequest(dataReq, false, false); err != nil {
		t.Fatalf("enqueue data: %v", err)
	}
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, nil, true, Options{
		RecvQueueSize:         1,
		CloseHandshakeTimeout: time.Second,
	})
	c.route = []byte("route-1")
	c.owner = o
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}

	o.sendRaw = func([]byte, []byte) error { return nil }
	var dataHead, controlHead *sendRequest
	o.flushQueues(&dataHead, &controlHead)
	select {
	case err := <-dataReq.result:
		if !errors.Is(err, errConnectionClosed) {
			t.Fatalf("data completion error = %v, want connection closed", err)
		}
	default:
		t.Fatal("Close did not cancel queued data while control budget was full")
	}
}

func TestServerFailCloseBlocksReplacementUntilQueuedDataCanceled(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	l := &listener{
		owner:   o,
		opts:    Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second},
		conns:   make(map[string]*conn),
		changed: make(chan struct{}),
	}
	c := newConn("old", transport.Endpoint{}, transport.Endpoint{}, l, true, l.opts)
	c.owner = o
	c.route = []byte("route-1")
	l.conns[string(c.route)] = c
	reservation, err := o.dataBudget.acquire(context.Background(), 4)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	req := &sendRequest{
		ctx:         context.Background(),
		route:       append([]byte(nil), c.route...),
		raw:         []byte("data"),
		result:      make(chan error, 1),
		reservation: reservation,
	}
	if err := o.enqueueRequest(req, false, false); err != nil {
		t.Fatalf("enqueue data: %v", err)
	}
	c.failLocal(errors.New("peer failed"))
	actions := l.handleIncoming(c.route, &protocol.Frame{
		Type:      protocol.FrameRequest,
		StreamID:  "replacement-stream",
		Direction: protocol.DirectionClientToServer,
	})
	if len(actions) != 1 || actions[0].frame.Status == nil || actions[0].frame.Status.Code != status.Unavailable {
		t.Fatalf("request during route cancellation actions = %#v, want Unavailable reset", actions)
	}
	o.sendRaw = func([]byte, []byte) error { return nil }
	var dataHead, controlHead *sendRequest
	o.flushQueues(&dataHead, &controlHead)
	select {
	case err := <-req.result:
		if !errors.Is(err, errConnectionClosed) {
			t.Fatalf("queued data error = %v, want connection closed", err)
		}
	default:
		t.Fatal("fail-close did not cancel queued route data")
	}
	if replacement, _ := l.connForRoute(c.route, true); replacement == nil {
		t.Fatal("replacement was not allowed after route cancellation completed")
	}
}

func TestRouteUnavailableFailClosesServerConn(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	l := &listener{owner: o, conns: make(map[string]*conn), changed: make(chan struct{})}
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, l, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.owner = o
	c.route = []byte("route-1")
	l.conns[string(c.route)] = c
	c.sendFrame = func(context.Context, *protocol.Frame) error { return zmq4.Errno(syscall.EHOSTUNREACH) }
	err := c.sendProtocolFrame(context.Background(), &protocol.Frame{Type: protocol.FramePong, Seq: 1})
	if !isRouteUnavailable(err) {
		t.Fatalf("sendProtocolFrame() error = %v, want route unavailable", err)
	}
	c.mu.Lock()
	closed := c.closeFinished
	c.mu.Unlock()
	if !closed || l.conns[string(c.route)] != nil {
		t.Fatalf("route unavailable left closed=%v mapped=%v", closed, l.conns[string(c.route)] != nil)
	}
}

func TestOwnerFatalSendRequestsTerminalFailure(t *testing.T) {
	o := newQueueTestOwner(t, 1, 16, 1)
	o.failCh = make(chan error, 1)
	reservation, err := o.dataBudget.acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("data acquire() error = %v", err)
	}
	wantErr := errors.New("socket failed")
	o.sendRaw = func([]byte, []byte) error { return wantErr }
	req := &sendRequest{ctx: context.Background(), raw: []byte("x"), reservation: reservation}
	if !o.trySend(req) {
		t.Fatal("trySend() did not complete fatal error")
	}
	select {
	case got := <-o.failCh:
		if !errors.Is(got, wantErr) {
			t.Fatalf("terminal error = %v, want %v", got, wantErr)
		}
	default:
		t.Fatal("fatal send did not request owner termination")
	}
}

func TestResetActionFailureClosesServerConnAndRemovesRoute(t *testing.T) {
	l := &listener{conns: make(map[string]*conn), changed: make(chan struct{})}
	c := newConn("server-1", transport.Endpoint{}, transport.Endpoint{}, l, true, Options{RecvQueueSize: 1, CloseHandshakeTimeout: time.Second})
	c.route = []byte("route-1")
	l.conns[string(c.route)] = c
	actions := c.resetActions("stream-1")
	if len(actions) != 1 || actions[0].onComplete == nil {
		t.Fatal("reset action has no completion callback")
	}
	actions[0].onComplete(errors.New("control send failed"))

	c.mu.Lock()
	closed := c.lifecycle.local == stateClosed
	c.mu.Unlock()
	if !closed {
		t.Fatal("control send failure did not close the connection")
	}
	l.mu.Lock()
	_, exists := l.conns[string(c.route)]
	l.mu.Unlock()
	if exists {
		t.Fatal("control send failure did not remove the server route")
	}
}

func newQueueTestOwner(t *testing.T, dataCount int, dataBytes int64, controlCount int) *owner {
	t.Helper()
	dataBudget, err := newSendBudget(dataCount, dataBytes)
	if err != nil {
		t.Fatalf("new data budget: %v", err)
	}
	controlBudget, err := newSendBudget(controlCount, 1<<20)
	if err != nil {
		t.Fatalf("new control budget: %v", err)
	}
	return &owner{
		dataCh:        make(chan *sendRequest, dataCount),
		controlCh:     make(chan *sendRequest, controlCount),
		dataBudget:    dataBudget,
		controlBudget: controlBudget,
		done:          make(chan struct{}),
	}
}
