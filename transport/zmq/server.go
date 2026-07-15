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
	// endpoint 是当前 ROUTER 绑定的地址。
	endpoint transport.Endpoint
	// owner 独占 ROUTER socket；listener/conn 不能直接操作 socket。
	owner *owner

	mu sync.Mutex
	// accepts 保存新出现的逻辑连接，等待 Listener.Accept 按 FIFO 取走。
	accepts []transport.Conn
	// conns 使用 ROUTER 收到的 DEALER identity 作为 key。
	// 同一个 identity 的后续 frame 都会进入同一个逻辑 conn。
	conns  map[string]*conn
	closed bool
	// changed 是 Accept 的广播唤醒信号。每次状态变化后关闭旧 channel 并替换新 channel。
	changed chan struct{}
}

func newListener(endpoint transport.Endpoint, opts Options) (transport.Listener, error) {
	if endpoint.Address == "" {
		return nil, errors.New("zmq: endpoint address is required")
	}
	endpoint = normalizeEndpoint(endpoint)
	// 服务端使用 ROUTER。ROUTER 能在收到消息时提供来源 identity，
	// 也能在发送时通过 identity 精确路由到指定 DEALER。
	zctx, socket, err := newSocket(zmq4.ROUTER, opts)
	if err != nil {
		return nil, err
	}
	// endpoint.Address 必须是 ZeroMQ 支持的 bind 地址，例如 tcp://*:5555 或 ipc:///tmp/zrpc.sock。
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
	// ROUTER 的所有收发都由 owner.run 串行处理。handleIncoming 只负责把已解码 frame
	// 转换成 zrpc 的逻辑 conn/stream，不直接碰 ZeroMQ socket。
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

		// Accept 是阻塞式接口：等待新 route 产生逻辑 conn、listener 关闭，或 ctx 取消。
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
	// 先在锁内摘出所有 conn，再在锁外逐个关闭，避免 closeLocal 回调/唤醒与 listener 锁交叉。
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
	// 最后关闭 owner，从而关闭底层 ROUTER socket 和 ZeroMQ context。
	return l.owner.close(ctx)
}

func (l *listener) handleIncoming(route []byte, frame *protocol.Frame) {
	if frame == nil {
		return
	}
	conn := l.connForRoute(route)
	if conn == nil || frame.Type == protocol.FramePing {
		// Ping 只用于让 ROUTER 学到/确认 DEALER identity，不形成业务 stream。
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
	// 首次看到某个 DEALER identity 时，创建一个服务端逻辑 conn。
	// 注意：服务端 conn 不拥有 socket；它共享 listener.owner，并在发送时携带 route。
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
	// conn.Close 只移除该 route 对应的逻辑连接，不影响共享 ROUTER socket。
	delete(l.conns, string(conn.route))
}

func (l *listener) signalLocked() {
	// close channel 用作广播，比向 channel 发送单个值更适合唤醒多个等待中的 Accept。
	close(l.changed)
	l.changed = make(chan struct{})
}
