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
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	errListenerClosed        = errors.New("zmq: listener closed")
	errConnectionClosed      = errors.New("zmq: connection closed")
	errConnectionDraining    = errors.New("zmq: connection draining")
	errCloseHandshakeTimeout = errors.New("zmq: close handshake timeout")
	errStreamClosed          = errors.New("zmq: stream closed")

	connSeq   atomic.Uint64
	streamSeq atomic.Uint64
)

type conn struct {
	// id 是本地逻辑连接 ID；客户端通常与 DEALER identity 相同。
	id     string
	local  transport.Endpoint
	remote transport.Endpoint
	// listener 仅服务端侧 conn 持有，用于 Close 时从 route map 中移除。
	listener *listener
	// owner 是实际 ZeroMQ socket 的拥有者。服务端多个 conn 会共享同一个 ROUTER owner。
	owner *owner
	// route 是 ROUTER 回复该连接时使用的 DEALER identity；客户端侧为空。
	route []byte
	// server 标识该 conn 是否是服务端 route 上的逻辑连接。
	server  bool
	metrics metrics.Collector

	mu sync.Mutex
	// incoming 保存已经由 FrameRequest 创建、等待 server.AcceptStream 消费的新 stream。
	incoming []transport.TransportStream
	// streams 按 StreamID 保存当前连接上的活跃 stream。
	streams               map[string]*stream
	lifecycle             connLifecycle
	controlSeq            uint64
	closeStarted          bool
	closeFinished         bool
	closeSeq              uint64
	closeAck              chan struct{}
	closeDone             chan struct{}
	closeErr              error
	closeHandshakeTimeout time.Duration
	heartbeat             heartbeatState
	heartbeatInterval     time.Duration
	peerTimeout           time.Duration
	peerTimedOut          bool
	drainSent             bool
	// sendFrame is injectable so lifecycle behavior can be tested without a socket.
	sendFrame func(context.Context, *protocol.Frame) error
	// changed 用于唤醒等待 AcceptStream 的 goroutine。
	changed       chan struct{}
	recvQueueSize int
}

func newConn(id string, local, remote transport.Endpoint, listener *listener, server bool, opts Options) *conn {
	return newConnWithMetrics(id, local, remote, listener, server, opts, true)
}

func newConnWithMetrics(id string, local, remote transport.Endpoint, listener *listener, server bool, opts Options, reportOpen bool) *conn {
	c := &conn{
		id:                    id,
		local:                 local,
		remote:                remote,
		listener:              listener,
		server:                server,
		metrics:               opts.Metrics,
		streams:               map[string]*stream{},
		lifecycle:             newConnLifecycle(),
		changed:               make(chan struct{}),
		recvQueueSize:         opts.RecvQueueSize,
		closeHandshakeTimeout: opts.CloseHandshakeTimeout,
		heartbeat:             newHeartbeatState(time.Now()),
		heartbeatInterval:     opts.HeartbeatInterval,
		peerTimeout:           opts.PeerTimeout,
	}
	if reportOpen {
		c.emitConnectionDelta(1, nil)
	}
	return c
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
	// OpenStream 先发送 FrameRequest。上层 unary 会随后发送 FrameData；
	// stream 调用则在 metadata 中额外带 rpc-mode=stream。
	err := c.sendProtocolFrame(ctx, &protocol.Frame{
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
		if len(c.incoming) > 0 {
			// FIFO 返回对端新打开的 stream。当前 incoming 尚未接入 RecvQueueSize 上限。
			stream := c.incoming[0]
			c.incoming[0] = nil
			c.incoming = c.incoming[1:]
			c.mu.Unlock()
			return stream, nil
		}
		if c.lifecycle.local >= stateClosing || c.lifecycle.peer >= stateClosing {
			c.mu.Unlock()
			return nil, errConnectionClosed
		}
		if !c.lifecycle.canAccept() {
			c.mu.Unlock()
			return nil, errConnectionDraining
		}
		changed := c.changed
		c.mu.Unlock()

		// 等待 routeFrame 创建新 stream、Drain/Close 改变状态，或 ctx 取消。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (c *conn) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closeFinished {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	started := c.closeStarted
	var streams []*stream
	if !started {
		c.closeStarted = true
		c.lifecycle.startLocalClose()
		c.closeSeq = c.nextControlSeqLocked()
		c.closeAck = make(chan struct{})
		c.closeDone = make(chan struct{})
		streams = c.detachStreamsLocked()
		c.signalLocked()
	}
	seq := c.closeSeq
	ack := c.closeAck
	done := c.closeDone
	c.mu.Unlock()

	for _, stream := range streams {
		c.emitStreamDelta(stream.id, -1)
		stream.closeLocal()
	}
	if !started {
		if c.owner != nil {
			c.owner.markRouteClosing(c.route)
		}
		go c.performClose(ctx, seq, ack)
	}
	select {
	case <-done:
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *conn) Drain(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.lifecycle.local >= stateClosing {
		c.mu.Unlock()
		return errConnectionClosed
	}
	if c.lifecycle.local == stateDraining && c.drainSent {
		c.mu.Unlock()
		return nil
	}
	c.lifecycle.startLocalDrain()
	seq := c.nextControlSeqLocked()
	c.signalLocked()
	c.mu.Unlock()
	err := c.sendProtocolFrame(ctx, &protocol.Frame{Type: protocol.FrameGoAway, Seq: seq})
	if err == nil {
		c.mu.Lock()
		c.drainSent = true
		c.mu.Unlock()
	}
	return err
}

func (c *conn) addStream(stream *stream) error {
	c.mu.Lock()
	if c.lifecycle.local >= stateClosing || c.lifecycle.peer >= stateClosing {
		c.mu.Unlock()
		return errConnectionClosed
	}
	if !c.lifecycle.canOpen() {
		c.mu.Unlock()
		return errConnectionDraining
	}
	c.streams[stream.id] = stream
	c.mu.Unlock()
	c.emitStreamDelta(stream.id, 1)
	return nil
}

func (c *conn) removeStream(id string) {
	c.mu.Lock()
	_, exists := c.streams[id]
	if exists {
		delete(c.streams, id)
	}
	c.mu.Unlock()
	if exists {
		c.emitStreamDelta(id, -1)
	}
}

func (c *conn) routeFrame(frame *protocol.Frame) []routeFrameAction {
	if frame == nil {
		return nil
	}
	switch frame.Type {
	case protocol.FrameGoAway:
		c.handleGoAway()
		return nil
	case protocol.FrameClose:
		return c.handlePeerClose(frame.Seq)
	case protocol.FrameCloseAck:
		c.handleCloseAck(frame.Seq)
		return nil
	case protocol.FramePing:
		emitTransportEvent(c.metrics, metrics.TransportEvent{
			Kind:         metrics.TransportHeartbeatPong,
			Value:        1,
			ConnectionID: c.id,
		})
		return c.controlActions(&protocol.Frame{Type: protocol.FramePong, Seq: frame.Seq})
	case protocol.FramePong:
		return nil
	}

	c.mu.Lock()
	if c.lifecycle.local >= stateClosing || c.lifecycle.peer >= stateClosing {
		c.mu.Unlock()
		return nil
	}
	if stream := c.streams[frame.StreamID]; stream != nil {
		c.mu.Unlock()
		// 已知 stream 的后续 frame 直接入队，等待对应 RecvFrame 消费。
		return stream.enqueueFrame(frame)
	}
	if frame.Type != protocol.FrameRequest {
		c.mu.Unlock()
		return nil
	}
	if !c.lifecycle.canAccept() {
		c.mu.Unlock()
		return c.unavailableResetActions(frame.StreamID)
	}
	if len(c.incoming) >= c.recvQueueSize {
		c.mu.Unlock()
		return c.resetActions(frame.StreamID)
	}
	// 首个 request frame 会创建服务端侧 stream，后续 data/response frame 再按 StreamID 路由。
	stream := newStream(frame.StreamID, c)
	c.streams[stream.id] = stream
	c.incoming = append(c.incoming, stream)
	c.signalLocked()
	c.mu.Unlock()
	c.emitStreamDelta(stream.id, 1)
	return stream.enqueueFrame(frame)
}

func (c *conn) closeLocal() {
	c.finishClose(nil)
}

func (c *conn) signalLocked() {
	// 使用 close channel 作为广播信号；替换新 channel 后，下一批等待者会等待新的状态变化。
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *conn) resetActions(streamID string) []routeFrameAction {
	return c.controlActions(receiveQueueFullReset(streamID))
}

func (c *conn) unavailableResetActions(streamID string) []routeFrameAction {
	return c.controlActions(&protocol.Frame{
		Type:     protocol.FrameReset,
		StreamID: streamID,
		Status:   &status.Status{Code: status.Unavailable, Message: "connection is draining"},
	})
}

func (c *conn) controlActions(frame *protocol.Frame) []routeFrameAction {
	return []routeFrameAction{{
		connectionID: c.id,
		route:        append([]byte(nil), c.route...),
		frame:        frame,
		onComplete: func(err error) {
			if err != nil {
				c.failLocal(err)
			}
		},
	}}
}

func (c *conn) failLocal(cause error) {
	if c.owner != nil {
		c.owner.markRouteClosing(c.route)
	}
	c.finishClose(cause)
	if c.listener != nil {
		c.listener.removeConn(c)
	}
	if !c.server && c.owner != nil {
		c.owner.requestFailure(cause)
	}
}

func (c *conn) sendProtocolFrame(ctx context.Context, frame *protocol.Frame) error {
	var err error
	if c.sendFrame != nil {
		err = c.sendFrame(ctx, frame)
	} else if c.owner == nil {
		err = errOwnerClosed
	} else {
		err = c.owner.sendForConnection(ctx, c.id, c.route, frame)
	}
	if isRouteUnavailable(err) {
		c.failLocal(err)
	}
	return err
}

func (c *conn) observeInbound(now time.Time) {
	c.mu.Lock()
	if !c.closeFinished {
		c.heartbeat.observe(now)
	}
	c.mu.Unlock()
}

func (c *conn) heartbeatActions(now time.Time) []routeFrameAction {
	c.mu.Lock()
	if c.lifecycle.local >= stateClosing || c.lifecycle.peer >= stateClosing {
		c.mu.Unlock()
		return nil
	}
	if c.heartbeat.timedOut(now, c.peerTimeout) {
		if c.peerTimedOut {
			c.mu.Unlock()
			return nil
		}
		c.peerTimedOut = true
		c.mu.Unlock()
		cause := status.Error(status.Unavailable, "zmq: peer heartbeat timeout")
		emitTransportEvent(c.metrics, metrics.TransportEvent{
			Kind:         metrics.TransportPeerTimeout,
			Value:        1,
			ConnectionID: c.id,
			Error:        cause,
		})
		c.failLocal(cause)
		return nil
	}
	if !c.heartbeat.shouldPing(now, c.heartbeatInterval) {
		c.mu.Unlock()
		return nil
	}
	seq := c.heartbeat.pendingPing
	c.mu.Unlock()
	emitTransportEvent(c.metrics, metrics.TransportEvent{
		Kind:         metrics.TransportHeartbeatPing,
		Value:        1,
		ConnectionID: c.id,
	})
	return c.controlActions(&protocol.Frame{Type: protocol.FramePing, Seq: seq})
}

func (c *conn) emitStreamDelta(streamID string, value int64) {
	emitTransportEvent(c.metrics, metrics.TransportEvent{
		Kind:         metrics.TransportStreamDelta,
		Value:        value,
		ConnectionID: c.id,
		StreamID:     streamID,
	})
}

func (c *conn) emitConnectionDelta(value int64, err error) {
	emitTransportEvent(c.metrics, metrics.TransportEvent{
		Kind:         metrics.TransportConnectionDelta,
		Value:        value,
		ConnectionID: c.id,
		Error:        err,
	})
}

func (c *conn) nextControlSeqLocked() uint64 {
	c.controlSeq++
	return c.controlSeq
}

func (c *conn) detachStreamsLocked() []*stream {
	streams := make([]*stream, 0, len(c.streams))
	for _, stream := range c.streams {
		streams = append(streams, stream)
	}
	c.streams = map[string]*stream{}
	c.incoming = nil
	return streams
}

func (c *conn) performClose(parent context.Context, seq uint64, ack <-chan struct{}) {
	timeout := c.closeHandshakeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	err := c.sendProtocolFrame(ctx, &protocol.Frame{Type: protocol.FrameClose, Seq: seq})
	if err == nil {
		var ownerDone <-chan struct{}
		if c.owner != nil {
			ownerDone = c.owner.done
		}
		select {
		case <-ack:
		case <-ctx.Done():
			if parentErr := parent.Err(); parentErr != nil {
				err = parentErr
			} else {
				err = errCloseHandshakeTimeout
			}
		case <-ownerDone:
			err = errOwnerClosed
		}
	} else if errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil {
		err = errCloseHandshakeTimeout
	}
	cancel()

	if c.server {
		if c.listener != nil {
			c.listener.removeConn(c)
		}
	} else if c.owner != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), timeout)
		if closeErr := c.owner.close(closeCtx); err == nil {
			err = closeErr
		}
		closeCancel()
	}
	c.finishClose(err)
}

func (c *conn) handleGoAway() {
	c.mu.Lock()
	if c.lifecycle.peer < stateClosing {
		c.lifecycle.markPeerDraining()
		c.signalLocked()
	}
	c.mu.Unlock()
}

func (c *conn) handleCloseAck(seq uint64) {
	c.mu.Lock()
	if c.closeStarted && seq == c.closeSeq && c.closeAck != nil {
		close(c.closeAck)
		c.closeAck = nil
	}
	c.mu.Unlock()
}

func (c *conn) handlePeerClose(seq uint64) []routeFrameAction {
	c.mu.Lock()
	if c.lifecycle.peer == stateClosed {
		c.mu.Unlock()
		return nil
	}
	localCloseAlreadyStarted := c.closeStarted
	if !c.closeStarted {
		c.closeStarted = true
		c.closeDone = make(chan struct{})
		c.lifecycle.startLocalClose()
	}
	c.lifecycle.markPeerClosing()
	streams := c.detachStreamsLocked()
	c.signalLocked()
	c.mu.Unlock()
	for _, stream := range streams {
		c.emitStreamDelta(stream.id, -1)
		stream.closeLocal()
	}
	return []routeFrameAction{{
		connectionID: c.id,
		route:        append([]byte(nil), c.route...),
		frame:        &protocol.Frame{Type: protocol.FrameCloseAck, Seq: seq},
		onComplete: func(err error) {
			if localCloseAlreadyStarted {
				if err != nil {
					c.failLocal(err)
				}
				return
			}
			c.finishPeerClose(err)
		},
	}}
}

func (c *conn) finishPeerClose(err error) {
	c.finishClose(err)
	if c.listener != nil {
		c.listener.removeConn(c)
	}
	if !c.server && c.owner != nil {
		c.owner.requestFailure(errOwnerClosed)
	}
}

func (c *conn) finishClose(err error) {
	c.mu.Lock()
	if c.closeFinished {
		c.mu.Unlock()
		return
	}
	c.closeStarted = true
	c.closeFinished = true
	c.closeErr = err
	c.lifecycle.markClosed()
	streams := c.detachStreamsLocked()
	if c.closeDone == nil {
		c.closeDone = make(chan struct{})
	}
	done := c.closeDone
	c.signalLocked()
	close(done)
	c.mu.Unlock()
	for _, stream := range streams {
		c.emitStreamDelta(stream.id, -1)
		stream.closeLocal()
	}
	c.emitConnectionDelta(-1, err)
}

type stream struct {
	id   string
	conn *conn

	mu sync.Mutex
	// frames 是该 stream 已收到但尚未被 RecvFrame 消费的有界 frame 队列。
	frames []*protocol.Frame
	closed bool
	// changed 用于唤醒等待 RecvFrame 的 goroutine。
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
	// 发送最终委托给 conn.owner，保证 ZeroMQ socket I/O 串行。
	return s.conn.sendProtocolFrame(ctx, frame)
}

func (s *stream) RecvFrame(ctx context.Context) (*protocol.Frame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if len(s.frames) > 0 {
			// 返回 clone，避免调用方修改队列中保存的 frame 或影响其他 goroutine。
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

		// changed channel 每次状态变化都会被替换，避免条件变量丢唤醒。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *stream) Close(ctx context.Context) error {
	// Close 是本地关闭，不发送 FrameEnd；业务 half-close 由上层 zrpc.Stream.CloseSend 发送 FrameEnd。
	s.closeLocal()
	s.conn.removeStream(s.id)
	return nil
}

func (s *stream) Reset(ctx context.Context, st *status.Status) error {
	// Reset 会先通知对端，再关闭并从本地 conn.streams 移除。
	err := s.SendFrame(ctx, &protocol.Frame{
		Type:     protocol.FrameReset,
		StreamID: s.id,
		Status:   cloneStatus(st),
	})
	s.closeLocal()
	s.conn.removeStream(s.id)
	return err
}

func (s *stream) enqueueFrame(frame *protocol.Frame) []routeFrameAction {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if !canBypassRecvQueue(frame) && len(s.frames) >= s.conn.recvQueueSize {
		s.closed = true
		conn := s.conn
		s.signalLocked()
		s.mu.Unlock()
		if conn != nil {
			conn.removeStream(s.id)
			return conn.resetActions(s.id)
		}
		return nil
	}
	// 入队时 clone frame，隔离 owner goroutine 与业务 goroutine 的内存所有权。
	s.frames = append(s.frames, cloneFrame(frame))
	s.signalLocked()
	s.mu.Unlock()
	return nil
}

func (s *stream) closeLocal() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	// 丢弃未消费 frame，等待中的 RecvFrame 会被唤醒并返回 errStreamClosed。
	s.frames = nil
	s.signalLocked()
	s.mu.Unlock()
}

func (s *stream) signalLocked() {
	// 与 conn.changed 一样，close 当前 channel 广播唤醒，再创建下一代 channel。
	close(s.changed)
	s.changed = make(chan struct{})
}

func newSocket(socketType zmq4.Type, opts Options) (*zmq4.Context, *zmq4.Socket, error) {
	// 每个 socket 使用独立 ZeroMQ context，便于 owner close 时完整释放资源。
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
	// HWM 是 libzmq 层队列边界；Go 层 conn/stream 队列还需要单独限制。
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
		// ROUTER_MANDATORY 让不可路由 identity 以错误形式暴露给调用方，而不是静默丢弃。
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
	// 编码前 clone，保证 msgpack 编码期间 frame 不会被调用方并发修改。
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
	// 解码后 clone，统一 frame ownership：调用方拿到的是可独占使用的副本。
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
