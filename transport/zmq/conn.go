package zmq

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	errListenerClosed     = errors.New("zmq: listener closed")
	errConnectionClosed   = errors.New("zmq: connection closed")
	errConnectionDraining = errors.New("zmq: connection draining")
	errStreamClosed       = errors.New("zmq: stream closed")
	errOwnerClosed        = errors.New("zmq: owner closed")

	connSeq   atomic.Uint64
	streamSeq atomic.Uint64
)

type owner struct {
	socket   *zmq4.Socket
	context  *zmq4.Context
	isRouter bool
	incoming func(route []byte, frame *protocol.Frame)

	sendCh  chan sendRequest
	closeCh chan closeRequest
	done    chan struct{}
}

type sendRequest struct {
	ctx    context.Context
	route  []byte
	frame  *protocol.Frame
	result chan error
}

type closeRequest struct {
	result chan error
}

func newOwner(zctx *zmq4.Context, socket *zmq4.Socket, isRouter bool, opts Options, incoming func(route []byte, frame *protocol.Frame)) *owner {
	o := &owner{
		socket:   socket,
		context:  zctx,
		isRouter: isRouter,
		incoming: incoming,
		sendCh:   make(chan sendRequest, opts.SendQueueSize),
		closeCh:  make(chan closeRequest),
		done:     make(chan struct{}),
	}
	go o.run()
	return o
}

func (o *owner) send(ctx context.Context, route []byte, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	req := sendRequest{
		ctx:    ctx,
		route:  append([]byte(nil), route...),
		frame:  cloneFrame(frame),
		result: make(chan error, 1),
	}
	select {
	case o.sendCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-o.done:
		return errOwnerClosed
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
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *owner) run() {
	defer close(o.done)
	poller := zmq4.NewPoller()
	poller.Add(o.socket, zmq4.POLLIN)
	var pending []sendRequest
	for {
		var closeReq *closeRequest
		pending, closeReq = o.drainControl(pending)
		if closeReq != nil {
			o.finish(closeReq, pending)
			return
		}
		pending = o.flushPending(pending)
		polled, err := poller.Poll(5 * time.Millisecond)
		if err != nil && !isAgain(err) {
			o.failPending(pending, err)
			_ = o.socket.Close()
			_ = o.context.Term()
			return
		}
		for _, event := range polled {
			if event.Socket == o.socket && event.Events&zmq4.POLLIN != 0 {
				o.recvAvailable()
			}
		}
	}
}

func (o *owner) drainControl(pending []sendRequest) ([]sendRequest, *closeRequest) {
	for {
		select {
		case req := <-o.sendCh:
			pending = append(pending, req)
		case req := <-o.closeCh:
			return pending, &req
		default:
			return pending, nil
		}
	}
}

func (o *owner) flushPending(pending []sendRequest) []sendRequest {
	for len(pending) > 0 {
		req := pending[0]
		if err := req.ctx.Err(); err != nil {
			req.result <- err
			pending[0] = sendRequest{}
			pending = pending[1:]
			continue
		}
		err := o.sendNow(req)
		if isAgain(err) {
			return pending
		}
		req.result <- err
		pending[0] = sendRequest{}
		pending = pending[1:]
	}
	return pending
}

func (o *owner) sendNow(req sendRequest) error {
	raw, err := encodeFrame(req.frame)
	if err != nil {
		return err
	}
	if o.isRouter {
		if len(req.route) == 0 {
			return errors.New("zmq: router send route is required")
		}
		_, err = o.socket.SendMessageDontwait(req.route, raw)
		return err
	}
	_, err = o.socket.SendMessageDontwait(raw)
	return err
}

func (o *owner) recvAvailable() {
	for {
		parts, err := o.socket.RecvMessageBytes(zmq4.DONTWAIT)
		if isAgain(err) {
			return
		}
		if err != nil {
			return
		}
		route, raw, ok := o.parseMessage(parts)
		if !ok {
			continue
		}
		frame, err := decodeFrame(raw)
		if err != nil {
			continue
		}
		o.incoming(route, frame)
	}
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

func (o *owner) finish(req *closeRequest, pending []sendRequest) {
	o.failPending(pending, errOwnerClosed)
	err := o.socket.Close()
	if termErr := o.context.Term(); err == nil {
		err = termErr
	}
	req.result <- err
}

func (o *owner) failPending(pending []sendRequest, err error) {
	for _, req := range pending {
		req.result <- err
	}
}

type conn struct {
	id       string
	local    transport.Endpoint
	remote   transport.Endpoint
	listener *listener
	owner    *owner
	route    []byte
	server   bool

	mu       sync.Mutex
	incoming []transport.TransportStream
	streams  map[string]*stream
	closed   bool
	draining bool
	changed  chan struct{}
}

func newConn(id string, local, remote transport.Endpoint, listener *listener, server bool) *conn {
	return &conn{
		id:       id,
		local:    local,
		remote:   remote,
		listener: listener,
		server:   server,
		streams:  map[string]*stream{},
		changed:  make(chan struct{}),
	}
}

func (c *conn) ID() string {
	return c.id
}

func (c *conn) LocalEndpoint() transport.Endpoint {
	return c.local
}

func (c *conn) RemoteEndpoint() transport.Endpoint {
	return c.remote
}

func (c *conn) OpenStream(ctx context.Context, method string, md metadata.MD) (transport.TransportStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "" {
		return nil, errors.New("zmq: method is required")
	}
	stream := newStream(nextStreamID(), c)
	if err := c.addStream(stream); err != nil {
		stream.closeLocal()
		return nil, err
	}
	requestMD := md.Copy()
	requestMD.Set(transport.MethodMetadataKey, method)
	err := c.owner.send(ctx, c.route, &protocol.Frame{
		Type:      protocol.FrameRequest,
		StreamID:  stream.id,
		Direction: protocol.DirectionClientToServer,
		Metadata:  requestMD,
	})
	if err != nil {
		c.removeStream(stream.id)
		stream.closeLocal()
		return nil, err
	}
	return stream, nil
}

func (c *conn) AcceptStream(ctx context.Context) (transport.TransportStream, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errConnectionClosed
		}
		if len(c.incoming) > 0 {
			stream := c.incoming[0]
			c.incoming[0] = nil
			c.incoming = c.incoming[1:]
			c.mu.Unlock()
			return stream, nil
		}
		if c.draining {
			c.mu.Unlock()
			return nil, errConnectionDraining
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (c *conn) Close(ctx context.Context) error {
	if c.server {
		c.closeLocal()
		if c.listener != nil {
			c.listener.removeConn(c)
		}
		return nil
	}
	c.closeLocal()
	if c.owner == nil {
		return nil
	}
	return c.owner.close(ctx)
}

func (c *conn) Drain(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConnectionClosed
	}
	c.draining = true
	c.signalLocked()
	return nil
}

func (c *conn) addStream(stream *stream) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errConnectionClosed
	}
	if c.draining {
		return errConnectionDraining
	}
	c.streams[stream.id] = stream
	return nil
}

func (c *conn) removeStream(id string) {
	c.mu.Lock()
	delete(c.streams, id)
	c.mu.Unlock()
}

func (c *conn) routeFrame(frame *protocol.Frame) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if stream := c.streams[frame.StreamID]; stream != nil {
		c.mu.Unlock()
		stream.enqueueFrame(frame)
		return
	}
	if frame.Type != protocol.FrameRequest {
		c.mu.Unlock()
		return
	}
	stream := newStream(frame.StreamID, c)
	c.streams[stream.id] = stream
	c.incoming = append(c.incoming, stream)
	c.signalLocked()
	c.mu.Unlock()
	stream.enqueueFrame(frame)
}

func (c *conn) closeLocal() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	streams := make([]*stream, 0, len(c.streams))
	for _, stream := range c.streams {
		streams = append(streams, stream)
	}
	c.streams = map[string]*stream{}
	c.incoming = nil
	c.signalLocked()
	c.mu.Unlock()

	for _, stream := range streams {
		stream.closeLocal()
	}
}

func (c *conn) signalLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

type stream struct {
	id   string
	conn *conn

	mu      sync.Mutex
	frames  []*protocol.Frame
	closed  bool
	changed chan struct{}
}

func newStream(id string, conn *conn) *stream {
	return &stream{id: id, conn: conn, changed: make(chan struct{})}
}

func (s *stream) ID() string {
	return s.id
}

func (s *stream) SendFrame(ctx context.Context, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if frame == nil {
		return errors.New("zmq: frame is nil")
	}
	if frame.StreamID != s.id {
		return fmt.Errorf("zmq: frame stream id %q does not match stream %q", frame.StreamID, s.id)
	}
	if err := frame.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errStreamClosed
	}
	return s.conn.owner.send(ctx, s.conn.route, frame)
}

func (s *stream) RecvFrame(ctx context.Context) (*protocol.Frame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if len(s.frames) > 0 {
			frame := s.frames[0]
			s.frames[0] = nil
			s.frames = s.frames[1:]
			s.mu.Unlock()
			return cloneFrame(frame), nil
		}
		if s.closed {
			s.mu.Unlock()
			return nil, errStreamClosed
		}
		changed := s.changed
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *stream) Close(ctx context.Context) error {
	s.closeLocal()
	s.conn.removeStream(s.id)
	return nil
}

func (s *stream) Reset(ctx context.Context, st *status.Status) error {
	err := s.SendFrame(ctx, &protocol.Frame{
		Type:     protocol.FrameReset,
		StreamID: s.id,
		Status:   cloneStatus(st),
	})
	s.closeLocal()
	s.conn.removeStream(s.id)
	return err
}

func (s *stream) enqueueFrame(frame *protocol.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.frames = append(s.frames, cloneFrame(frame))
	s.signalLocked()
}

func (s *stream) closeLocal() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.frames = nil
	s.signalLocked()
	s.mu.Unlock()
}

func (s *stream) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func newSocket(socketType zmq4.Type, opts Options) (*zmq4.Context, *zmq4.Socket, error) {
	zctx, err := zmq4.NewContext()
	if err != nil {
		return nil, nil, err
	}
	socket, err := zctx.NewSocket(socketType)
	if err != nil {
		_ = zctx.Term()
		return nil, nil, err
	}
	if err := applySocketOptions(socket, socketType, opts); err != nil {
		_ = socket.Close()
		_ = zctx.Term()
		return nil, nil, err
	}
	return zctx, socket, nil
}

func applySocketOptions(socket *zmq4.Socket, socketType zmq4.Type, opts Options) error {
	if err := socket.SetSndhwm(opts.SndHWM); err != nil {
		return err
	}
	if err := socket.SetRcvhwm(opts.RcvHWM); err != nil {
		return err
	}
	if err := socket.SetLinger(opts.Linger); err != nil {
		return err
	}
	if err := socket.SetImmediate(opts.Immediate); err != nil {
		return err
	}
	if socketType == zmq4.ROUTER && opts.RouterMandatory {
		if err := socket.SetRouterMandatory(1); err != nil {
			return err
		}
	}
	return nil
}

func encodeFrame(frame *protocol.Frame) ([]byte, error) {
	if frame == nil {
		return nil, errors.New("zmq: frame is nil")
	}
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	return msgpack.Marshal(cloneFrame(frame))
}

func decodeFrame(raw []byte) (*protocol.Frame, error) {
	var frame protocol.Frame
	if err := msgpack.Unmarshal(raw, &frame); err != nil {
		return nil, err
	}
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	return cloneFrame(&frame), nil
}

func cloneFrame(frame *protocol.Frame) *protocol.Frame {
	if frame == nil {
		return nil
	}
	return &protocol.Frame{
		Type:      frame.Type,
		StreamID:  frame.StreamID,
		Seq:       frame.Seq,
		Direction: frame.Direction,
		Metadata:  frame.Metadata.Copy(),
		Payload:   append([]byte(nil), frame.Payload...),
		Window:    frame.Window,
		Status:    cloneStatus(frame.Status),
	}
}

func cloneStatus(st *status.Status) *status.Status {
	if st == nil {
		return nil
	}
	return &status.Status{
		Code:    st.Code,
		Message: st.Message,
		Details: append([]string(nil), st.Details...),
	}
}

func normalizeEndpoint(endpoint transport.Endpoint) transport.Endpoint {
	if endpoint.Transport == "" {
		endpoint.Transport = "zmq"
	}
	return endpoint
}

func nextConnID(prefix string) string {
	id := connSeq.Add(1)
	return prefix + "-" + strconv.FormatUint(id, 10)
}

func nextStreamID() string {
	id := streamSeq.Add(1)
	return "stream-" + strconv.FormatUint(id, 10)
}

func isAgain(err error) bool {
	return err != nil && zmq4.AsErrno(err) == zmq4.Errno(syscall.EAGAIN)
}
