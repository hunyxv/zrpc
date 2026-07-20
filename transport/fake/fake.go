package fake

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

var (
	errListenerClosed     = errors.New("fake: listener closed")
	errListenerNotFound   = errors.New("fake: listener not found")
	errConnectionClosed   = errors.New("fake: connection closed")
	errConnectionDraining = errors.New("fake: connection draining")
	errPeerClosed         = errors.New("fake: peer closed")
	errStreamClosed       = errors.New("fake: stream closed")
	errPeerStreamClosed   = errors.New("fake: peer stream closed")
)

const defaultRecvQueueSize = 1024

// Options 描述 fake transport 的测试运行参数。
type Options struct {
	// RecvQueueSize 是单连接 incoming stream 队列和单 stream frame 队列的最大 frame 数。
	// 小于等于 0 时使用默认值 1024。
	RecvQueueSize int
}

func defaultOptions(opts Options) Options {
	if opts.RecvQueueSize <= 0 {
		opts.RecvQueueSize = defaultRecvQueueSize
	}
	return opts
}

// Transport 是内存版 transport，用于不依赖网络的测试。
type Transport struct {
	mu         sync.Mutex
	listeners  map[string]*listener
	nextConn   atomic.Uint64
	nextStream atomic.Uint64
	opts       Options
}

// New 创建新的内存 transport。
func New(opts ...Options) *Transport {
	options := Options{}
	if len(opts) > 0 {
		options = opts[0]
	}
	return &Transport{listeners: map[string]*listener{}, opts: defaultOptions(options)}
}

// Name 返回 transport 名称。
func (t *Transport) Name() string {
	return "fake"
}

// Listen 注册一个内存 listener。
func (t *Transport) Listen(endpoint transport.Endpoint, opts transport.ListenOptions) (transport.Listener, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.listeners[endpoint.Address]; exists {
		return nil, errors.New("fake: listener already exists")
	}
	l := &listener{
		transport: t,
		endpoint:  endpoint,
		changed:   make(chan struct{}),
	}
	t.listeners[endpoint.Address] = l
	return l, nil
}

// Dial 连接到同一 Transport 实例中已注册的 listener。
func (t *Transport) Dial(ctx context.Context, endpoint transport.Endpoint, opts transport.DialOptions) (transport.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t.mu.Lock()
	l := t.listeners[endpoint.Address]
	t.mu.Unlock()
	if l == nil {
		return nil, errListenerNotFound
	}

	clientID := t.nextConnID("client")
	serverID := t.nextConnID("server")
	serverEndpoint := endpoint
	if serverEndpoint.Transport == "" {
		serverEndpoint.Transport = t.Name()
	}
	clientEndpoint := transport.Endpoint{Transport: serverEndpoint.Transport, Address: clientID}
	client := newConn(t, clientID, clientEndpoint, serverEndpoint)
	server := newConn(t, serverID, serverEndpoint, clientEndpoint)
	client.peer = server
	server.peer = client

	if err := l.enqueue(ctx, server); err != nil {
		client.closeLocal()
		server.closeLocal()
		return nil, err
	}
	return client, nil
}

func (t *Transport) nextConnID(prefix string) string {
	id := t.nextConn.Add(1)
	return prefix + "-" + strconv.FormatUint(id, 10)
}

func (t *Transport) nextStreamID() string {
	id := t.nextStream.Add(1)
	return "stream-" + strconv.FormatUint(id, 10)
}

type listener struct {
	transport *Transport
	endpoint  transport.Endpoint

	mu      sync.Mutex
	accepts []transport.Conn
	closed  bool
	changed chan struct{}
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return nil, errListenerClosed
		}
		if len(l.accepts) > 0 {
			conn := l.accepts[0]
			l.accepts[0] = nil
			l.accepts = l.accepts[1:]
			l.mu.Unlock()
			return conn, nil
		}
		changed := l.changed
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (l *listener) Close(ctx context.Context) error {
	l.transport.mu.Lock()
	delete(l.transport.listeners, l.endpoint.Address)
	l.transport.mu.Unlock()

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	queued := append([]transport.Conn(nil), l.accepts...)
	l.accepts = nil
	l.signalLocked()
	l.mu.Unlock()

	for _, queuedConn := range queued {
		if c, ok := queuedConn.(*conn); ok {
			c.closeCascade()
			continue
		}
		_ = queuedConn.Close(ctx)
	}
	return nil
}

func (l *listener) enqueue(ctx context.Context, conn transport.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errListenerClosed
	}
	l.accepts = append(l.accepts, conn)
	l.signalLocked()
	return nil
}

func (l *listener) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

type conn struct {
	transport *Transport
	id        string
	local     transport.Endpoint
	remote    transport.Endpoint
	peer      *conn

	mu       sync.Mutex
	incoming []transport.TransportStream
	streams  map[*stream]struct{}
	closed   bool
	draining bool
	changed  chan struct{}
}

func newConn(t *Transport, id string, local, remote transport.Endpoint) *conn {
	return &conn{
		transport: t,
		id:        id,
		local:     local,
		remote:    remote,
		streams:   map[*stream]struct{}{},
		changed:   make(chan struct{}),
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
		return nil, errors.New("fake: method is required")
	}
	if c.peer == nil {
		return nil, errPeerClosed
	}

	id := c.transport.nextStreamID()
	left := newStream(id)
	right := newStream(id)
	left.peer = right
	right.peer = left

	if err := c.addStream(left); err != nil {
		left.closeBoth()
		return nil, err
	}
	requestMD := md.Copy()
	requestMD.Set(transport.MethodMetadataKey, method)
	requestFrame := &protocol.Frame{
		Type:      protocol.FrameRequest,
		StreamID:  id,
		Direction: protocol.DirectionClientToServer,
		Metadata:  requestMD,
	}
	if err := right.enqueueFrame(ctx, requestFrame); err != nil {
		c.removeStream(left)
		left.closeBoth()
		return nil, err
	}
	if err := c.peer.enqueueStream(ctx, right); err != nil {
		c.removeStream(left)
		left.closeBoth()
		return nil, err
	}
	return left, nil
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
	c.closeCascade()
	return nil
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
	stream.owner = c
	c.streams[stream] = struct{}{}
	return nil
}

func (c *conn) removeStream(stream *stream) {
	c.mu.Lock()
	if c.streams != nil {
		delete(c.streams, stream)
	}
	c.mu.Unlock()
}

func (c *conn) enqueueStream(ctx context.Context, stream *stream) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errPeerClosed
	}
	if c.draining {
		return errConnectionDraining
	}
	if len(c.incoming) >= c.transport.opts.RecvQueueSize {
		if stream.peer != nil {
			_ = stream.peer.enqueueTerminal(ctx, receiveQueueFullReset(stream.id))
		}
		stream.closeLocal(true)
		return nil
	}
	c.incoming = append(c.incoming, stream)
	stream.owner = c
	c.streams[stream] = struct{}{}
	c.signalLocked()
	return nil
}

func (c *conn) closeCascade() {
	if c == nil {
		return
	}
	peer := c.peer
	c.closeLocal()
	if peer != nil {
		peer.closeLocal()
	}
}

func (c *conn) closeLocal() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.draining = true
	c.incoming = nil
	streams := make([]*stream, 0, len(c.streams))
	for stream := range c.streams {
		streams = append(streams, stream)
	}
	c.streams = nil
	c.signalLocked()
	c.mu.Unlock()

	for _, stream := range streams {
		stream.closeBoth()
	}
}

func (c *conn) signalLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

type stream struct {
	id    string
	peer  *stream
	owner *conn

	mu      sync.Mutex
	frames  []*protocol.Frame
	closed  bool
	changed chan struct{}
}

func newStream(id string) *stream {
	return &stream{id: id, changed: make(chan struct{})}
}

func (s *stream) ID() string {
	return s.id
}

func (s *stream) SendFrame(ctx context.Context, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closedState() {
		return errStreamClosed
	}
	if s.peer == nil {
		return errPeerStreamClosed
	}
	if frame == nil {
		return errors.New("fake: frame is nil")
	}
	if frame.StreamID != s.id {
		return errors.New("fake: frame stream id mismatch")
	}
	return s.peer.enqueueFrame(ctx, frame)
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
	s.closeLocal(true)
	return nil
}

func (s *stream) Reset(ctx context.Context, st *status.Status) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closedState() {
		return errStreamClosed
	}
	if s.peer == nil {
		return errPeerStreamClosed
	}
	resetFrame := &protocol.Frame{Type: protocol.FrameReset, StreamID: s.id, Status: st}
	if err := s.peer.enqueueTerminal(ctx, resetFrame); err != nil {
		return err
	}
	s.closeLocal(true)
	return nil
}

func (s *stream) enqueueFrame(ctx context.Context, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cloned, err := cloneValidatedFrame(frame)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// A real asynchronous transport may accept late flow-control frames after
		// the peer has reclaimed its local stream. Treat them as delivered so a
		// successfully received payload is not turned into a transport error.
		if cloned.Type == protocol.FrameWindowUpdate {
			return nil
		}
		return errPeerStreamClosed
	}
	if !canBypassRecvQueue(cloned) && len(s.frames) >= s.recvQueueSize() {
		s.closed = true
		owner := s.owner
		peer := s.peer
		s.owner = nil
		s.signalLocked()
		s.mu.Unlock()
		if owner != nil {
			owner.removeStream(s)
		}
		if peer != nil {
			_ = peer.enqueueTerminal(ctx, receiveQueueFullReset(s.id))
		}
		return nil
	}
	s.frames = append(s.frames, cloned)
	s.signalLocked()
	s.mu.Unlock()
	return nil
}

func (s *stream) enqueueTerminal(ctx context.Context, frame *protocol.Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cloned, err := cloneValidatedFrame(frame)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errPeerStreamClosed
	}
	s.closed = true
	s.frames = []*protocol.Frame{cloned}
	owner := s.owner
	s.owner = nil
	s.signalLocked()
	s.mu.Unlock()
	if owner != nil {
		owner.removeStream(s)
	}
	return nil
}

func (s *stream) recvQueueSize() int {
	if s.owner == nil || s.owner.transport == nil {
		return defaultRecvQueueSize
	}
	return s.owner.transport.opts.RecvQueueSize
}

func canBypassRecvQueue(frame *protocol.Frame) bool {
	return frame != nil && (frame.Type == protocol.FrameEnd || frame.Type == protocol.FrameReset)
}

func receiveQueueFullReset(streamID string) *protocol.Frame {
	return &protocol.Frame{
		Type:     protocol.FrameReset,
		StreamID: streamID,
		Status:   &status.Status{Code: status.ResourceExhausted, Message: "transport receive queue full"},
	}
}

func (s *stream) closeBoth() {
	s.closeLocal(true)
	if s.peer != nil {
		s.peer.closeLocal(true)
	}
}

func (s *stream) closeLocal(clearFrames bool) {
	s.mu.Lock()
	if clearFrames {
		s.frames = nil
	}
	owner := s.owner
	s.owner = nil
	if !s.closed {
		s.closed = true
		s.signalLocked()
	}
	s.mu.Unlock()
	if owner != nil {
		owner.removeStream(s)
	}
}

func (s *stream) closedState() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *stream) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func cloneValidatedFrame(frame *protocol.Frame) (*protocol.Frame, error) {
	if frame == nil {
		return nil, errors.New("fake: frame is nil")
	}
	cloned := cloneFrame(frame)
	if err := cloned.Validate(); err != nil {
		return nil, err
	}
	return cloned, nil
}

func cloneFrame(frame *protocol.Frame) *protocol.Frame {
	if frame == nil {
		return nil
	}
	cloned := *frame
	if frame.Metadata != nil {
		cloned.Metadata = frame.Metadata.Copy()
	}
	cloned.Payload = append([]byte(nil), frame.Payload...)
	if frame.Status != nil {
		st := *frame.Status
		st.Details = append([]string(nil), frame.Status.Details...)
		cloned.Status = &st
	}
	return &cloned
}
