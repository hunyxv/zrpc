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

type Transport struct {
	mu         sync.Mutex
	listeners  map[string]*listener
	nextConn   uint64
	nextStream uint64
}

func New() *Transport {
	return &Transport{listeners: map[string]*listener{}}
}

func (t *Transport) Name() string {
	return "fake"
}

func (t *Transport) Listen(endpoint transport.Endpoint, opts transport.ListenOptions) (transport.Listener, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.listeners[endpoint.Address]; exists {
		return nil, errors.New("fake: listener already exists")
	}
	l := &listener{
		transport: t,
		endpoint:  endpoint,
		accepts:   make(chan transport.Conn, 16),
		done:      make(chan struct{}),
	}
	t.listeners[endpoint.Address] = l
	return l, nil
}

func (t *Transport) Dial(ctx context.Context, endpoint transport.Endpoint, opts transport.DialOptions) (transport.Conn, error) {
	t.mu.Lock()
	l := t.listeners[endpoint.Address]
	t.mu.Unlock()
	if l == nil {
		return nil, errors.New("fake: listener not found")
	}
	if l.closed() {
		return nil, errors.New("fake: listener closed")
	}

	client := newConn(t, t.nextConnID("client"), endpoint)
	server := newConn(t, t.nextConnID("server"), endpoint)
	client.peer = server
	server.peer = client

	select {
	case l.accepts <- server:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, errors.New("fake: listener closed")
	}
}

func (t *Transport) nextConnID(prefix string) string {
	id := atomic.AddUint64(&t.nextConn, 1)
	return prefix + "-" + strconv.FormatUint(id, 10)
}

func (t *Transport) nextStreamID() string {
	id := atomic.AddUint64(&t.nextStream, 1)
	return "stream-" + strconv.FormatUint(id, 10)
}

type listener struct {
	transport *Transport
	endpoint  transport.Endpoint
	accepts   chan transport.Conn
	done      chan struct{}
	once      sync.Once
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
	if l.closed() {
		return nil, errors.New("fake: listener closed")
	}
	select {
	case conn := <-l.accepts:
		if conn == nil {
			return nil, errors.New("fake: listener closed")
		}
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, errors.New("fake: listener closed")
	}
}

func (l *listener) Close(ctx context.Context) error {
	l.once.Do(func() {
		l.transport.mu.Lock()
		delete(l.transport.listeners, l.endpoint.Address)
		l.transport.mu.Unlock()
		close(l.done)
	})
	return nil
}

func (l *listener) closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

type conn struct {
	transport *Transport
	id        string
	endpoint  transport.Endpoint
	peer      *conn
	streams   chan transport.TransportStream
	done      chan struct{}
	once      sync.Once
}

func newConn(t *Transport, id string, endpoint transport.Endpoint) *conn {
	return &conn{transport: t, id: id, endpoint: endpoint, streams: make(chan transport.TransportStream, 16), done: make(chan struct{})}
}

func (c *conn) ID() string {
	return c.id
}

func (c *conn) LocalEndpoint() transport.Endpoint {
	return c.endpoint
}

func (c *conn) RemoteEndpoint() transport.Endpoint {
	return c.endpoint
}

func (c *conn) OpenStream(ctx context.Context, method string, md metadata.MD) (transport.TransportStream, error) {
	if c.peer == nil {
		return nil, errors.New("fake: connection has no peer")
	}
	if c.closed() {
		return nil, errors.New("fake: connection closed")
	}
	if c.peer.closed() {
		return nil, errors.New("fake: peer connection closed")
	}
	id := c.transport.nextStreamID()
	left := newStream(id)
	right := newStream(id)
	left.peer = right
	right.peer = left
	select {
	case c.peer.streams <- right:
		return left, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("fake: connection closed")
	case <-c.peer.done:
		return nil, errors.New("fake: peer connection closed")
	}
}

func (c *conn) AcceptStream(ctx context.Context) (transport.TransportStream, error) {
	if c.closed() {
		return nil, errors.New("fake: connection closed")
	}
	select {
	case stream := <-c.streams:
		if stream == nil {
			return nil, errors.New("fake: connection closed")
		}
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("fake: connection closed")
	}
}

func (c *conn) Close(ctx context.Context) error {
	c.once.Do(func() {
		close(c.done)
	})
	return nil
}

func (c *conn) Drain(ctx context.Context) error {
	return nil
}

func (c *conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

type stream struct {
	id     string
	frames chan *protocol.Frame
	peer   *stream
	done   chan struct{}
	once   sync.Once
}

func newStream(id string) *stream {
	return &stream{id: id, frames: make(chan *protocol.Frame, 16), done: make(chan struct{})}
}

func (s *stream) ID() string {
	return s.id
}

func (s *stream) SendFrame(ctx context.Context, frame *protocol.Frame) error {
	if s.peer == nil {
		return errors.New("fake: stream has no peer")
	}
	if s.closed() {
		return errors.New("fake: stream closed")
	}
	if s.peer.closed() {
		return errors.New("fake: peer stream closed")
	}
	select {
	case s.peer.frames <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("fake: stream closed")
	case <-s.peer.done:
		return errors.New("fake: peer stream closed")
	}
}

func (s *stream) RecvFrame(ctx context.Context) (*protocol.Frame, error) {
	select {
	case frame := <-s.frames:
		if frame == nil {
			return nil, errors.New("fake: stream closed")
		}
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, errors.New("fake: stream closed")
	}
}

func (s *stream) Close(ctx context.Context) error {
	s.once.Do(func() {
		close(s.done)
	})
	return nil
}

func (s *stream) Reset(ctx context.Context, st *status.Status) error {
	return s.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameReset, StreamID: s.id, Status: st})
}

func (s *stream) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
