package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	rpctrace "github.com/hunyxv/zrpc/trace"
	"github.com/hunyxv/zrpc/transport"
)

// Server 是 zrpc 服务端，负责监听 transport、接受 stream 并分派到 handler。
type Server struct {
	opts Options

	mu     sync.RWMutex
	unary  map[string]zrpc.UnaryHandler
	stream map[string]zrpc.StreamHandler
	conns  map[transport.Conn]struct{}

	streamSlots chan struct{}
}

// New 创建服务端实例。
func New(opts Options) *Server {
	if opts.Metrics == nil {
		opts.Metrics = metrics.Noop()
	}
	var streamSlots chan struct{}
	if opts.MaxConcurrentStreams > 0 {
		streamSlots = make(chan struct{}, opts.MaxConcurrentStreams)
	}
	return &Server{
		opts:        opts,
		unary:       map[string]zrpc.UnaryHandler{},
		stream:      map[string]zrpc.StreamHandler{},
		conns:       map[transport.Conn]struct{}{},
		streamSlots: streamSlots,
	}
}

// HandleUnary 注册请求-响应 RPC handler。
func (s *Server) HandleUnary(method string, handler zrpc.UnaryHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unary[method] = handler
}

// HandleStream 注册流式 RPC handler。
func (s *Server) HandleStream(method string, handler zrpc.StreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stream[method] = handler
}

// Serve 启动服务端监听并阻塞处理连接，直到 ctx 取消或 listener 返回错误。
func (s *Server) Serve(ctx context.Context) error {
	if s.opts.Transport == nil {
		return errors.New("zrpc/server: transport is required")
	}
	if s.opts.Codec == nil {
		return errors.New("zrpc/server: codec is required")
	}
	listener, err := s.opts.Transport.Listen(s.opts.Endpoint, transport.ListenOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close(context.Background())
		s.closeConns(context.Background())
	}()

	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return err
		}
		s.addConn(conn)
		// 每条连接由独立 goroutine 驱动；后续需要在这里接入并发准入控制。
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn transport.Conn) {
	defer func() {
		_ = conn.Close(context.Background())
		s.removeConn(conn)
	}()
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		if !s.tryAcquireStream() {
			_ = stream.Reset(ctx, &status.Status{Code: status.ResourceExhausted, Message: "too many concurrent streams"})
			_ = stream.Close(context.Background())
			continue
		}
		// 每个 transport stream 独立执行业务 handler，避免单个长流阻塞同连接其他 stream。
		go s.serveAcceptedStream(ctx, stream)
	}
}

func (s *Server) serveAcceptedStream(ctx context.Context, stream transport.TransportStream) {
	defer s.releaseStream()
	defer func() { _ = stream.Close(context.Background()) }()
	s.serveUnaryStream(ctx, stream)
}

func (s *Server) tryAcquireStream() bool {
	if s.streamSlots == nil {
		return true
	}
	select {
	case s.streamSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseStream() {
	if s.streamSlots == nil {
		return
	}
	<-s.streamSlots
}

func (s *Server) serveUnaryStream(ctx context.Context, stream transport.TransportStream) {
	requestFrame, err := stream.RecvFrame(ctx)
	if err != nil {
		return
	}
	if requestFrame.Type != protocol.FrameRequest {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.InvalidArgument, "expected request frame")
		return
	}
	method := requestFrame.Metadata.Get(transport.MethodMetadataKey)
	if method == "" {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.InvalidArgument, "method is required")
		return
	}
	ctx = rpctrace.Extract(ctx, requestFrame.Metadata)
	if handler := s.streamHandler(method); handler != nil {
		rpcStream := zrpc.NewInternalStream(ctx, method, requestFrame.Metadata, s.opts.Codec, stream, s.opts.InitialStreamWindow, protocol.DirectionServerToClient)
		if err := handler.HandleStream(ctx, rpcStream); err != nil {
			_ = rpcStream.Reset(ctx, err)
			return
		}
		_ = rpcStream.CloseSend(ctx)
		return
	}
	if requestFrame.Metadata.Get(transport.ModeMetadataKey) == transport.ModeStream {
		_ = stream.Reset(ctx, &status.Status{Code: status.Unimplemented, Message: fmt.Sprintf("unknown method %q", method)})
		return
	}

	bodyFrame, err := stream.RecvFrame(ctx)
	if err != nil {
		return
	}
	if bodyFrame.Type != protocol.FrameData {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.InvalidArgument, "expected data frame")
		return
	}

	req, err := zrpc.NewRequestBytes(method, requestFrame.Metadata, bodyFrame.Payload, s.opts.Codec)
	if err != nil {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.InvalidArgument, err.Error())
		return
	}
	resp, err := s.invokeUnary(ctx, req)
	st := status.FromError(err)
	out := &protocol.Frame{
		Type:      protocol.FrameResponse,
		StreamID:  requestFrame.StreamID,
		Direction: protocol.DirectionServerToClient,
		Status:    &st,
	}
	if resp != nil {
		out.Metadata = resp.Metadata
		out.Payload = resp.Body
	}
	_ = stream.SendFrame(ctx, out)
}

func (s *Server) invokeUnary(ctx context.Context, req *zrpc.Request) (resp *zrpc.Response, err error) {
	info := metrics.RPCInfo{Method: req.Method}
	s.opts.Metrics.OnRPCStart(ctx, info)
	start := time.Now()
	defer func() {
		st := status.FromError(err)
		s.opts.Metrics.OnRPCFinish(ctx, info, &st, time.Since(start))
	}()

	handler := s.unaryHandler(req.Method)
	if handler == nil {
		return nil, status.Error(status.Unimplemented, fmt.Sprintf("unknown method %q", req.Method))
	}
	return handler.HandleUnary(ctx, req)
}

func (s *Server) invokeUnaryForTest(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
	return s.invokeUnary(ctx, req)
}

func (s *Server) unaryHandler(method string) zrpc.UnaryHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unary[method]
}

func (s *Server) streamHandler(method string) zrpc.StreamHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stream[method]
}

func (s *Server) sendStatus(ctx context.Context, stream transport.TransportStream, streamID string, code status.Code, message string) error {
	return stream.SendFrame(ctx, &protocol.Frame{
		Type:      protocol.FrameResponse,
		StreamID:  streamID,
		Direction: protocol.DirectionServerToClient,
		Status:    &status.Status{Code: code, Message: message},
	})
}

func (s *Server) addConn(conn transport.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[conn] = struct{}{}
}

func (s *Server) removeConn(conn transport.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) closeConns(ctx context.Context) {
	s.mu.RLock()
	conns := make([]transport.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.RUnlock()
	for _, conn := range conns {
		_ = conn.Close(ctx)
	}
}
