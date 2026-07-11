package zmq

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

type listener struct {
	endpoint transport.Endpoint
	owner    *owner

	mu      sync.Mutex
	accepts []transport.Conn
	conns   map[string]*conn
	closed  bool
	changed chan struct{}
}

func newListener(endpoint transport.Endpoint, opts Options) (transport.Listener, error) {
	if endpoint.Address == "" {
		return nil, errors.New("zmq: endpoint address is required")
	}
	endpoint = normalizeEndpoint(endpoint)
	zctx, socket, err := newSocket(zmq4.ROUTER, opts)
	if err != nil {
		return nil, err
	}
	if err := socket.Bind(endpoint.Address); err != nil {
		_ = socket.Close()
		_ = zctx.Term()
		return nil, err
	}
	l := &listener{
		endpoint: endpoint,
		conns:    map[string]*conn{},
		changed:  make(chan struct{}),
	}
	l.owner = newOwner(zctx, socket, true, opts, l.handleIncoming)
	return l, nil
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
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	conns := make([]*conn, 0, len(l.conns))
	for _, conn := range l.conns {
		conns = append(conns, conn)
	}
	l.conns = map[string]*conn{}
	l.accepts = nil
	l.signalLocked()
	l.mu.Unlock()

	for _, conn := range conns {
		conn.closeLocal()
	}
	return l.owner.close(ctx)
}

func (l *listener) handleIncoming(route []byte, frame *protocol.Frame) {
	if frame == nil {
		return
	}
	conn := l.connForRoute(route)
	if conn == nil || frame.Type == protocol.FramePing {
		return
	}
	conn.routeFrame(frame)
}

func (l *listener) connForRoute(route []byte) *conn {
	key := string(route)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if conn := l.conns[key]; conn != nil {
		return conn
	}
	routeCopy := append([]byte(nil), route...)
	remote := transport.Endpoint{Transport: "zmq", Address: hex.EncodeToString(routeCopy)}
	conn := newConn(nextConnID("server"), l.endpoint, remote, l, true)
	conn.route = routeCopy
	conn.owner = l.owner
	l.conns[key] = conn
	l.accepts = append(l.accepts, conn)
	l.signalLocked()
	return conn
}

func (l *listener) removeConn(conn *conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conns == nil {
		return
	}
	delete(l.conns, string(conn.route))
}

func (l *listener) signalLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}
