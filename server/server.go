package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

type Server struct {
	opts Options

	mu     sync.RWMutex
	unary  map[string]zrpc.UnaryHandler
	stream map[string]zrpc.StreamHandler
	conns  map[transport.Conn]struct{}
}

func New(opts Options) *Server {
	return &Server{
		opts:   opts,
		unary:  map[string]zrpc.UnaryHandler{},
		stream: map[string]zrpc.StreamHandler{},
		conns:  map[transport.Conn]struct{}{},
	}
}

func (s *Server) HandleUnary(method string, handler zrpc.UnaryHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unary[method] = handler
}

func (s *Server) HandleStream(method string, handler zrpc.StreamHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stream[method] = handler
}

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
		go s.serveUnaryStream(ctx, stream)
	}
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
	if handler := s.streamHandler(method); handler != nil {
		rpcStream := zrpc.NewInternalStream(ctx, method, requestFrame.Metadata, s.opts.Codec, stream, s.opts.InitialStreamWindow)
		if err := handler.HandleStream(ctx, rpcStream); err != nil {
			_ = rpcStream.Reset(ctx, err)
		}
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

	handler := s.unaryHandler(method)
	if handler == nil {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.Unimplemented, fmt.Sprintf("unknown method %q", method))
		return
	}

	req, err := zrpc.NewRequestBytes(method, requestFrame.Metadata, bodyFrame.Payload, s.opts.Codec)
	if err != nil {
		_ = s.sendStatus(ctx, stream, requestFrame.StreamID, status.InvalidArgument, err.Error())
		return
	}
	resp, err := handler.HandleUnary(ctx, req)
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
