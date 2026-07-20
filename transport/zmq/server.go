package zmq

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

type listener struct {
	// endpoint 是当前 ROUTER 绑定的地址。
	endpoint transport.Endpoint
	// recvQueueSize 限制每个逻辑连接和 stream 的 Go 层接收队列。
	recvQueueSize int
	opts          Options
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
	changed   chan struct{}
	sweepStop chan struct{}
	sweepDone chan struct{}
}

func newListener(endpoint transport.Endpoint, opts Options) (transport.Listener, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
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
		endpoint:      endpoint,
		recvQueueSize: opts.RecvQueueSize,
		opts:          opts,
		conns:         map[string]*conn{},
		changed:       make(chan struct{}),
		sweepStop:     make(chan struct{}),
		sweepDone:     make(chan struct{}),
	}
	// ROUTER 的所有收发都由 owner.run 串行处理。handleIncoming 只负责把已解码 frame
	// 转换成 zrpc 的逻辑 conn/stream，不直接碰 ZeroMQ socket。
	l.owner, err = newOwner(zctx, socket, true, opts, l.handleIncoming, nil, l.ownerFailed)
	if err != nil {
		_ = socket.Close()
		_ = zctx.Term()
		return nil, err
	}
	go l.runHeartbeatSweeper()
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
	close(l.sweepStop)
	l.signalLocked()
	l.mu.Unlock()
	<-l.sweepDone

	for _, conn := range conns {
		conn.closeLocal()
	}
	// 最后关闭 owner，从而关闭底层 ROUTER socket 和 ZeroMQ context。
	return l.owner.close(ctx)
}

func (l *listener) handleIncoming(route []byte, frame *protocol.Frame) []routeFrameAction {
	if frame == nil {
		return nil
	}
	allowCreate := frame.Type == protocol.FramePing || frame.Type == protocol.FrameRequest
	conn, cancelPending := l.connForRoute(route, allowCreate)
	if conn == nil {
		if cancelPending && frame.Type == protocol.FrameRequest {
			return []routeFrameAction{{
				route: append([]byte(nil), route...),
				frame: &protocol.Frame{
					Type:     protocol.FrameReset,
					StreamID: frame.StreamID,
					Status:   &status.Status{Code: status.Unavailable, Message: "previous route is still closing"},
				},
			}}
		}
		return nil
	}
	conn.observeInbound(time.Now())
	return conn.routeFrame(frame)
}

func (l *listener) connForRoute(route []byte, allowCreate bool) (*conn, bool) {
	key := string(route)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, false
	}
	if conn := l.conns[key]; conn != nil {
		l.mu.Unlock()
		return conn, false
	}
	if !allowCreate {
		l.mu.Unlock()
		return nil, false
	}
	if !l.owner.openRoute(route) {
		l.mu.Unlock()
		return nil, true
	}
	// 首次看到某个 DEALER identity 时，创建一个服务端逻辑 conn。
	// 注意：服务端 conn 不拥有 socket；它共享 listener.owner，并在发送时携带 route。
	routeCopy := append([]byte(nil), route...)
	remote := transport.Endpoint{Transport: "zmq", Address: hex.EncodeToString(routeCopy)}
	conn := newConnWithMetrics(nextConnID("server"), l.endpoint, remote, l, true, l.opts, false)
	conn.route = routeCopy
	conn.owner = l.owner
	l.conns[key] = conn
	l.accepts = append(l.accepts, conn)
	l.signalLocked()
	l.mu.Unlock()
	conn.emitConnectionDelta(1, nil)
	return conn, false
}

func (l *listener) removeConn(conn *conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conns == nil {
		return
	}
	// Delayed cleanup from an old conn must not remove a replacement that reused
	// the same DEALER identity.
	key := string(conn.route)
	if l.conns[key] == conn {
		delete(l.conns, key)
	}
}

func (l *listener) signalLocked() {
	// close channel 用作广播，比向 channel 发送单个值更适合唤醒多个等待中的 Accept。
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *listener) runHeartbeatSweeper() {
	defer close(l.sweepDone)
	ticker := time.NewTicker(l.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			l.sweepHeartbeats(now)
		case <-l.sweepStop:
			return
		}
	}
}

func (l *listener) ownerFailed(cause error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	conns := make([]*conn, 0, len(l.conns))
	for _, conn := range l.conns {
		conns = append(conns, conn)
	}
	l.conns = map[string]*conn{}
	l.accepts = nil
	close(l.sweepStop)
	l.signalLocked()
	l.mu.Unlock()
	for _, conn := range conns {
		conn.finishClose(cause)
	}
}

func (l *listener) sweepHeartbeats(now time.Time) {
	l.mu.Lock()
	conns := make([]*conn, 0, len(l.conns))
	for _, conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()
	for _, conn := range conns {
		for _, action := range conn.heartbeatActions(now) {
			l.owner.enqueueAction(action)
		}
	}
}
