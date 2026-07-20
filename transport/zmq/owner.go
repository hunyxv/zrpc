package zmq

import (
	"context"
	"errors"
	"math"
	"sync"
	"syscall"
	"time"

	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	zmq4 "github.com/pebbe/zmq4"
)

var errOwnerClosed = errors.New("zmq: owner closed")

const ownerControlBurst = 8

type owner struct {
	// socket can only be accessed by the goroutine running owner.run.
	socket              *zmq4.Socket
	context             *zmq4.Context
	isRouter            bool
	incoming            func(route []byte, frame *protocol.Frame) []routeFrameAction
	maintenance         func(time.Time) []routeFrameAction
	terminal            func(error)
	maintenanceInterval time.Duration
	nextMaintenance     time.Time
	sendRaw             func(route, raw []byte) error
	metrics             metrics.Collector

	dataCh        chan *sendRequest
	controlCh     chan *sendRequest
	dataBudget    *sendBudget
	controlBudget *sendBudget
	closeCh       chan closeRequest
	failCh        chan error
	done          chan struct{}

	// enqueueMu makes stopping the owner atomic with respect to queue admission.
	enqueueMu           sync.Mutex
	stopping            bool
	closingRoutes       map[string]struct{}
	pendingRouteCancels map[string]struct{}
}

type sendRequest struct {
	ctx          context.Context
	route        []byte
	raw          []byte
	result       chan error
	reservation  *sendReservation
	onComplete   func(error)
	connectionID string
	streamID     string
	once         sync.Once
}

func (r *sendRequest) complete(err error) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.reservation.release()
		if r.onComplete != nil {
			r.onComplete(err)
		}
		if r.result != nil {
			r.result <- err
		}
	})
}

type closeRequest struct {
	result chan error
}

type routeFrameAction struct {
	connectionID string
	route        []byte
	frame        *protocol.Frame
	onComplete   func(error)
}

func newOwner(zctx *zmq4.Context, socket *zmq4.Socket, isRouter bool, opts Options, incoming func(route []byte, frame *protocol.Frame) []routeFrameAction, maintenance func(time.Time) []routeFrameAction, terminal func(error)) (*owner, error) {
	dataBudget, err := newSendBudget(opts.SendQueueSize, opts.SendQueueBytes)
	if err != nil {
		return nil, err
	}
	controlBudget, err := newSendBudget(opts.ControlQueueSize, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	o := &owner{
		socket:              socket,
		context:             zctx,
		isRouter:            isRouter,
		incoming:            incoming,
		maintenance:         maintenance,
		terminal:            terminal,
		maintenanceInterval: opts.HeartbeatInterval,
		nextMaintenance:     time.Now().Add(opts.HeartbeatInterval),
		metrics:             opts.Metrics,
		dataCh:              make(chan *sendRequest, opts.SendQueueSize),
		controlCh:           make(chan *sendRequest, opts.ControlQueueSize),
		dataBudget:          dataBudget,
		controlBudget:       controlBudget,
		closeCh:             make(chan closeRequest, 1),
		failCh:              make(chan error, 1),
		done:                make(chan struct{}),
		closingRoutes:       make(map[string]struct{}),
		pendingRouteCancels: make(map[string]struct{}),
	}
	o.sendRaw = o.socketSend
	go o.run()
	return o, nil
}

func (o *owner) send(ctx context.Context, route []byte, frame *protocol.Frame) error {
	return o.sendForConnection(ctx, "", route, frame)
}

func (o *owner) sendForConnection(ctx context.Context, connectionID string, route []byte, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := encodeFrame(frame)
	if err != nil {
		return err
	}
	control := isControlFrame(frame.Type)
	budget := o.dataBudget
	if control {
		budget = o.controlBudget
	}
	reservation, err := budget.acquire(ctx, int64(len(raw)))
	if err != nil {
		o.emitQueueRejected(connectionID, frame.StreamID, err)
		return err
	}
	req := &sendRequest{
		ctx:          ctx,
		route:        append([]byte(nil), route...),
		raw:          raw,
		result:       make(chan error, 1),
		reservation:  reservation,
		connectionID: connectionID,
		streamID:     frame.StreamID,
	}
	barrier := frame.Type == protocol.FrameClose || frame.Type == protocol.FrameCloseAck
	if err := o.enqueueRequest(req, control, barrier); err != nil {
		req.complete(err)
		o.emitQueueRejected(connectionID, frame.StreamID, err)
		return err
	}
	select {
	case err := <-req.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-o.done:
		return errOwnerClosed
	}
}

func (o *owner) enqueue(queue chan *sendRequest, req *sendRequest) error {
	return o.enqueueRequest(req, queue == o.controlCh, false)
}

func (o *owner) enqueueRequest(req *sendRequest, control, barrier bool) error {
	o.enqueueMu.Lock()
	defer o.enqueueMu.Unlock()
	if o.stopping {
		return errOwnerClosed
	}
	if o.closingRoutes == nil {
		o.closingRoutes = make(map[string]struct{})
	}
	if o.pendingRouteCancels == nil {
		o.pendingRouteCancels = make(map[string]struct{})
	}
	key := string(req.route)
	if !control {
		if _, closed := o.closingRoutes[key]; closed {
			return errConnectionClosed
		}
	}
	if barrier {
		o.closingRoutes[key] = struct{}{}
		o.pendingRouteCancels[key] = struct{}{}
	}
	queue := o.dataCh
	if control {
		queue = o.controlCh
	}
	// A reservation guarantees a slot: queued requests plus the owner's heads
	// can never exceed the corresponding budget count.
	select {
	case queue <- req:
		return nil
	default:
		return status.Error(status.ResourceExhausted, "zmq: send queue is full")
	}
}

func (o *owner) close(ctx context.Context) error {
	req := closeRequest{result: make(chan error, 1)}
	select {
	case o.closeCh <- req:
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *owner) run() {
	defer close(o.done)
	poller := zmq4.NewPoller()
	poller.Add(o.socket, zmq4.POLLIN)
	var dataHead, controlHead *sendRequest
	for {
		select {
		case req := <-o.closeCh:
			o.finish(&req, dataHead, controlHead)
			return
		case err := <-o.failCh:
			o.finishFailed(dataHead, controlHead, err)
			return
		default:
		}

		o.flushQueues(&dataHead, &controlHead)
		if o.isStopping() {
			continue
		}

		polled, err := poller.Poll(5 * time.Millisecond)
		if err != nil && !isAgain(err) {
			o.finishFailed(dataHead, controlHead, err)
			return
		}
		for _, event := range polled {
			if event.Socket == o.socket && event.Events&zmq4.POLLIN != 0 {
				if err := o.recvAvailable(); err != nil {
					o.finishFailed(dataHead, controlHead, err)
					return
				}
			}
		}
		o.runMaintenance(time.Now())
	}
}

func (o *owner) runMaintenance(now time.Time) {
	if o.maintenance == nil || now.Before(o.nextMaintenance) {
		return
	}
	o.nextMaintenance = now.Add(o.maintenanceInterval)
	for _, action := range o.maintenance(now) {
		if action.frame != nil {
			o.enqueueAction(action)
		}
	}
}

func (o *owner) flushQueues(dataHead, controlHead **sendRequest) {
	o.applyPendingRouteBarriers(dataHead)
	for i := 0; i < ownerControlBurst; i++ {
		if *controlHead == nil {
			select {
			case *controlHead = <-o.controlCh:
			default:
			}
		}
		if *controlHead == nil {
			break
		}
		if !o.trySend(*controlHead) {
			break
		}
		*controlHead = nil
		if o.isStopping() {
			return
		}
	}
	o.applyPendingRouteBarriers(dataHead)

	if *dataHead == nil {
		select {
		case *dataHead = <-o.dataCh:
		default:
		}
	}
	if *dataHead != nil && o.trySend(*dataHead) {
		*dataHead = nil
	}
}

func (o *owner) applyPendingRouteBarriers(dataHead **sendRequest) {
	o.enqueueMu.Lock()
	if len(o.pendingRouteCancels) == 0 {
		o.enqueueMu.Unlock()
		return
	}
	routes := o.pendingRouteCancels
	o.pendingRouteCancels = make(map[string]struct{})
	var canceled []*sendRequest
	if *dataHead != nil {
		if _, ok := routes[string((*dataHead).route)]; ok {
			canceled = append(canceled, *dataHead)
			*dataHead = nil
		}
	}
	queued := len(o.dataCh)
	kept := make([]*sendRequest, 0, queued)
	for i := 0; i < queued; i++ {
		req := <-o.dataCh
		if _, ok := routes[string(req.route)]; ok {
			canceled = append(canceled, req)
		} else {
			kept = append(kept, req)
		}
	}
	for _, req := range kept {
		o.dataCh <- req
	}
	o.enqueueMu.Unlock()
	for _, req := range canceled {
		req.complete(errConnectionClosed)
	}
}

func (o *owner) openRoute(route []byte) bool {
	o.enqueueMu.Lock()
	defer o.enqueueMu.Unlock()
	if _, pending := o.pendingRouteCancels[string(route)]; pending {
		return false
	}
	delete(o.closingRoutes, string(route))
	return true
}

func (o *owner) trySend(req *sendRequest) bool {
	if err := req.ctx.Err(); err != nil {
		req.complete(err)
		return true
	}
	err := o.sendNow(req)
	if isAgain(err) {
		return false
	}
	if isRouteUnavailable(err) {
		o.markRouteClosing(req.route)
		emitTransportEvent(o.metrics, metrics.TransportEvent{
			Kind:         metrics.TransportRouteUnavailable,
			Value:        1,
			ConnectionID: req.connectionID,
			StreamID:     req.streamID,
			Error:        err,
		})
	} else if err != nil {
		o.requestFailure(err)
	}
	req.complete(err)
	return true
}

func (o *owner) sendNow(req *sendRequest) error {
	return o.sendRaw(req.route, req.raw)
}

func (o *owner) socketSend(route, raw []byte) error {
	if o.isRouter {
		if len(route) == 0 {
			return errors.New("zmq: router send route is required")
		}
		_, err := o.socket.SendMessageDontwait(route, raw)
		return err
	}
	_, err := o.socket.SendMessageDontwait(raw)
	return err
}

func (o *owner) recvAvailable() error {
	const maxReceiveBurst = 64
	for i := 0; i < maxReceiveBurst; i++ {
		parts, err := o.socket.RecvMessageBytes(zmq4.DONTWAIT)
		if isAgain(err) {
			return nil
		}
		if err != nil {
			return err
		}
		route, raw, ok := o.parseMessage(parts)
		if !ok {
			continue
		}
		frame, err := decodeFrame(raw)
		if err != nil {
			continue
		}
		for _, action := range o.incoming(route, frame) {
			if action.frame != nil {
				o.enqueueAction(action)
			}
		}
	}
	return nil
}

func (o *owner) enqueueAction(action routeFrameAction) bool {
	barrier := action.frame.Type == protocol.FrameClose || action.frame.Type == protocol.FrameCloseAck
	if barrier {
		o.markRouteClosing(action.route)
	}
	raw, err := encodeFrame(action.frame)
	if err != nil {
		o.completeAction(action, err)
		return false
	}
	reservation, ok := o.controlBudget.tryAcquire(int64(len(raw)))
	if !ok {
		err := status.Error(status.ResourceExhausted, "zmq: control queue is full")
		o.emitQueueRejected(action.connectionID, action.frame.StreamID, err)
		o.completeAction(action, err)
		return false
	}
	req := &sendRequest{
		ctx:          context.Background(),
		route:        append([]byte(nil), action.route...),
		raw:          raw,
		reservation:  reservation,
		onComplete:   action.onComplete,
		connectionID: action.connectionID,
		streamID:     action.frame.StreamID,
	}
	if err := o.enqueueRequest(req, true, barrier); err != nil {
		req.complete(err)
		o.emitQueueRejected(action.connectionID, action.frame.StreamID, err)
		return false
	}
	return true
}

func (o *owner) markRouteClosing(route []byte) {
	o.enqueueMu.Lock()
	if o.closingRoutes == nil {
		o.closingRoutes = make(map[string]struct{})
	}
	if o.pendingRouteCancels == nil {
		o.pendingRouteCancels = make(map[string]struct{})
	}
	key := string(route)
	o.closingRoutes[key] = struct{}{}
	o.pendingRouteCancels[key] = struct{}{}
	o.enqueueMu.Unlock()
}

func (o *owner) completeAction(action routeFrameAction, err error) {
	if action.onComplete != nil {
		action.onComplete(err)
	}
}

func (o *owner) emitQueueRejected(connectionID, streamID string, err error) {
	if status.FromError(err).Code != status.ResourceExhausted {
		return
	}
	emitTransportEvent(o.metrics, metrics.TransportEvent{
		Kind:         metrics.TransportQueueRejected,
		Value:        1,
		ConnectionID: connectionID,
		StreamID:     streamID,
		Error:        err,
	})
}

func (o *owner) parseMessage(parts [][]byte) ([]byte, []byte, bool) {
	if o.isRouter {
		if len(parts) < 2 {
			return nil, nil, false
		}
		return append([]byte(nil), parts[0]...), parts[len(parts)-1], true
	}
	if len(parts) == 0 {
		return nil, nil, false
	}
	return nil, parts[len(parts)-1], true
}

func (o *owner) finish(req *closeRequest, dataHead, controlHead *sendRequest) {
	o.beginStop()
	o.failAll(dataHead, controlHead, errOwnerClosed)
	err := o.socket.Close()
	if termErr := o.context.Term(); err == nil {
		err = termErr
	}
	req.result <- err
}

func (o *owner) finishFailed(dataHead, controlHead *sendRequest, cause error) {
	o.beginStop()
	o.failAll(dataHead, controlHead, cause)
	_ = o.socket.Close()
	_ = o.context.Term()
	if o.terminal != nil {
		o.terminal(cause)
	}
}

func (o *owner) requestFailure(cause error) {
	if cause == nil {
		cause = errOwnerClosed
	}
	o.beginStop()
	select {
	case o.failCh <- cause:
	default:
	}
}

func (o *owner) beginStop() {
	o.enqueueMu.Lock()
	o.stopping = true
	o.enqueueMu.Unlock()
}

func (o *owner) isStopping() bool {
	o.enqueueMu.Lock()
	stopping := o.stopping
	o.enqueueMu.Unlock()
	return stopping
}

func (o *owner) failAll(dataHead, controlHead *sendRequest, err error) {
	dataHead.complete(err)
	controlHead.complete(err)
	for {
		select {
		case req := <-o.dataCh:
			req.complete(err)
		default:
			goto control
		}
	}

control:
	for {
		select {
		case req := <-o.controlCh:
			req.complete(err)
		default:
			return
		}
	}
}

func isControlFrame(frameType protocol.FrameType) bool {
	switch frameType {
	case protocol.FrameWindowUpdate,
		protocol.FrameReset,
		protocol.FrameGoAway,
		protocol.FrameClose,
		protocol.FrameCloseAck,
		protocol.FramePing,
		protocol.FramePong:
		return true
	default:
		return false
	}
}

func isRouteUnavailable(err error) bool {
	return err != nil && zmq4.AsErrno(err) == zmq4.Errno(syscall.EHOSTUNREACH)
}
