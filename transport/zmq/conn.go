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
	// socket 只能由 owner.run 所在 goroutine 访问，避免多个 goroutine 并发操作 libzmq socket。
	socket   *zmq4.Socket
	context  *zmq4.Context
	isRouter bool
	// incoming 是 socket 收到并解码 frame 后的回调。
	// ROUTER 模式会携带 route，DEALER 模式 route 为空。
	incoming func(route []byte, frame *protocol.Frame) []routeFrameAction

	// sendCh 接收其他 goroutine 提交的发送请求。
	sendCh  chan sendRequest
	closeCh chan closeRequest
	// done 在 owner.run 完全退出后关闭，调用方可用它判断 socket 生命周期结束。
	done chan struct{}
}

type sendRequest struct {
	// ctx 控制排队和实际发送等待时间。
	ctx context.Context
	// route 仅 ROUTER 发送时使用，表示目标 DEALER identity。
	route []byte
	// frame 在进入 owner 前已经 clone，避免调用方并发修改。
	frame *protocol.Frame
	// result 把 socket 发送结果返回给调用 goroutine。
	result chan error
}

type closeRequest struct {
	result chan error
}

type routeFrameAction struct {
	route []byte
	frame *protocol.Frame
}

func newOwner(zctx *zmq4.Context, socket *zmq4.Socket, isRouter bool, opts Options, incoming func(route []byte, frame *protocol.Frame) []routeFrameAction) *owner {
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
	// 第一段 select 负责进入 owner 队列；队列满时由 ctx 控制等待上限。
	select {
	case o.sendCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-o.done:
		return errOwnerClosed
	}
	// 第二段 select 等待 owner goroutine 实际执行 socket send 后返回结果。
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
	// close 也必须交给 owner.run 执行，因为 socket/context 的关闭要和 send/recv 串行。
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
		// ZeroMQ socket 不是并发安全对象，所有 socket I/O 必须集中在 owner goroutine。
		// 每轮先吸收控制请求，再尝试刷出 pending，最后 poll 读事件。
		pending, closeReq = o.drainControl(pending)
		if closeReq != nil {
			o.finish(closeReq, pending)
			return
		}
		pending = o.flushPending(pending)
		// 当前 poll 使用固定 5ms 间隔；后续可改成可被 sendCh 唤醒的 reactor，降低空闲发送延迟。
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
			// 不在这里直接发送，是为了把控制队列 drain 干净后统一按 pending 顺序刷出。
			pending = append(pending, req)
		case req := <-o.closeCh:
			// close 优先级高于继续等待新发送；已有 pending 会在 finish 中统一失败。
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
			// 请求在排队期间已经超时或取消，直接回传 ctx 错误。
			req.result <- err
			pending[0] = sendRequest{}
			pending = pending[1:]
			continue
		}
		err := o.sendNow(req)
		if isAgain(err) {
			// ZeroMQ 当前不可写，保留 pending 队列，下一轮 poll/flush 再试。
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
		// ROUTER 发送必须显式带目标 route，ZeroMQ 会按该 identity 路由到对应 DEALER。
		_, err = o.socket.SendMessageDontwait(req.route, raw)
		return err
	}
	// DEALER 发送不需要 route，服务端 ROUTER 会从 multipart envelope 中拿到发送方 identity。
	_, err = o.socket.SendMessageDontwait(raw)
	return err
}

func (o *owner) recvAvailable() {
	for {
		// DONTWAIT 让 owner 一次性读空当前可读数据，直到 EAGAIN。
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
			// 坏 frame 直接丢弃。后续可接入 transport metrics 记录 decode 错误。
			continue
		}
		actions := o.incoming(route, frame)
		for _, action := range actions {
			if action.frame == nil {
				continue
			}
			_ = o.sendNow(sendRequest{route: action.route, frame: action.frame})
		}
	}
}

func (o *owner) parseMessage(parts [][]byte) ([]byte, []byte, bool) {
	if o.isRouter {
		if len(parts) < 2 {
			return nil, nil, false
		}
		// ROUTER 收到的第一段是 DEALER identity，最后一段是 zrpc frame。
		// 当前协议不使用空 delimiter，中间段如存在会被忽略。
		return append([]byte(nil), parts[0]...), parts[len(parts)-1], true
	}
	if len(parts) == 0 {
		return nil, nil, false
	}
	return nil, parts[len(parts)-1], true
}

func (o *owner) finish(req *closeRequest, pending []sendRequest) {
	// close 到达后不再发送 pending，全部返回 owner closed，避免关闭中的 socket 继续被使用。
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
	server bool

	mu sync.Mutex
	// incoming 保存已经由 FrameRequest 创建、等待 server.AcceptStream 消费的新 stream。
	incoming []transport.TransportStream
	// streams 按 StreamID 保存当前连接上的活跃 stream。
	streams map[string]*stream
	closed  bool
	// draining 表示连接不再接受新 stream，但已有 stream 可继续收尾。
	draining bool
	// changed 用于唤醒等待 AcceptStream 的 goroutine。
	changed       chan struct{}
	recvQueueSize int
}

func newConn(id string, local, remote transport.Endpoint, listener *listener, server bool, recvQueueSize int) *conn {
	return &conn{
		id:            id,
		local:         local,
		remote:        remote,
		listener:      listener,
		server:        server,
		streams:       map[string]*stream{},
		changed:       make(chan struct{}),
		recvQueueSize: recvQueueSize,
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
	// OpenStream 先发送 FrameRequest。上层 unary 会随后发送 FrameData；
	// stream 调用则在 metadata 中额外带 rpc-mode=stream。
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
			// FIFO 返回对端新打开的 stream。当前 incoming 尚未接入 RecvQueueSize 上限。
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

		// 等待 routeFrame 创建新 stream、Drain/Close 改变状态，或 ctx 取消。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (c *conn) Close(ctx context.Context) error {
	if c.server {
		// 服务端 conn 只是 ROUTER 上某个 route 的逻辑连接，不能关闭共享 ROUTER socket。
		c.closeLocal()
		if c.listener != nil {
			c.listener.removeConn(c)
		}
		return nil
	}
	// 客户端 conn 拥有独立 DEALER owner，关闭连接时需要关闭 socket/context。
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
	// 唤醒正在 AcceptStream 的 goroutine，让它看到 draining 状态并返回错误。
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

func (c *conn) routeFrame(frame *protocol.Frame) []routeFrameAction {
	c.mu.Lock()
	if c.closed {
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
	return stream.enqueueFrame(frame)
}

func (c *conn) closeLocal() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	// closeLocal 会关闭当前 conn 上所有 stream，并唤醒 AcceptStream。
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
	// 使用 close channel 作为广播信号；替换新 channel 后，下一批等待者会等待新的状态变化。
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *conn) resetActions(streamID string) []routeFrameAction {
	return []routeFrameAction{{
		route: append([]byte(nil), c.route...),
		frame: receiveQueueFullReset(streamID),
	}}
}

type stream struct {
	id   string
	conn *conn

	mu sync.Mutex
	// frames 是该 stream 已收到但尚未被 RecvFrame 消费的 frame 队列。
	// 当前队列无上限，后续需要接入 RecvQueueSize 或 per-stream queue limit。
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
	return s.conn.owner.send(ctx, s.conn.route, frame)
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
