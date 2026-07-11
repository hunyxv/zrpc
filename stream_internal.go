package zrpc

import (
	"context"
	"io"
	"sync"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

const defaultStreamWindow = 1024 * 1024

type rpcStream struct {
	ctx    context.Context
	method string
	md     metadata.MD
	codec  codec.Codec
	ts     transport.TransportStream
	dir    protocol.Direction

	sendWin *protocol.Window
	recvWin *protocol.Window

	mu          sync.Mutex
	sendDone    bool
	recvDone    bool
	terminalErr error
}

func NewInternalStream(ctx context.Context, method string, md metadata.MD, c codec.Codec, ts transport.TransportStream, window int, direction ...protocol.Direction) Stream {
	if window <= 0 {
		window = defaultStreamWindow
	}
	dir := protocol.DirectionNone
	if len(direction) > 0 {
		dir = direction[0]
	}
	return &rpcStream{
		ctx:     ctx,
		method:  method,
		md:      md.Copy(),
		codec:   c,
		ts:      ts,
		dir:     dir,
		sendWin: protocol.NewWindow(window),
		recvWin: protocol.NewWindow(window),
	}
}

func (s *rpcStream) Context() context.Context {
	return s.ctx
}

func (s *rpcStream) Method() string {
	return s.method
}

func (s *rpcStream) Metadata() metadata.MD {
	return s.md.Copy()
}

func (s *rpcStream) Send(ctx context.Context, msg any) error {
	raw, err := s.codec.Marshal(msg)
	if err != nil {
		return err
	}
	if err := s.sendWin.Acquire(ctx, len(raw)); err != nil {
		return err
	}
	defer func() {
		_ = s.sendWin.Release(len(raw))
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.sendDone {
		return status.Error(status.FailedPrecondition, "stream send side closed")
	}
	return s.ts.SendFrame(ctx, &protocol.Frame{
		Type:      protocol.FrameData,
		StreamID:  s.ts.ID(),
		Direction: s.dir,
		Payload:   raw,
	})
}

func (s *rpcStream) Recv(ctx context.Context, msg any) error {
	if err := s.recvState(); err != nil {
		return err
	}
	frame, err := s.ts.RecvFrame(ctx)
	if err != nil {
		return err
	}
	switch frame.Type {
	case protocol.FrameData:
		return s.codec.Unmarshal(frame.Payload, msg)
	case protocol.FrameWindowUpdate:
		_ = s.sendWin.Release(frame.Window)
		return s.Recv(ctx, msg)
	case protocol.FrameEnd:
		s.markRecvDone()
		return io.EOF
	case protocol.FrameReset:
		err := status.Error(status.Unknown, "stream reset")
		if frame.Status != nil {
			err = status.WithDetails(status.Error(frame.Status.Code, frame.Status.Message), frame.Status.Details...)
		}
		s.markTerminal(err)
		return err
	default:
		return status.Error(status.Internal, "unexpected stream frame")
	}
}

func (s *rpcStream) CloseSend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.sendDone {
		return nil
	}
	s.sendDone = true
	return s.ts.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameEnd, StreamID: s.ts.ID(), Direction: s.dir})
}

func (s *rpcStream) CloseRecv(ctx context.Context) error {
	s.mu.Lock()
	s.recvDone = true
	closeTransport := s.sendDone
	s.mu.Unlock()
	if closeTransport {
		return s.ts.Close(ctx)
	}
	return nil
}

func (s *rpcStream) Reset(ctx context.Context, err error) error {
	st := status.FromError(err)
	terminalErr := status.WithDetails(status.Error(st.Code, st.Message), st.Details...)
	s.setTerminal(terminalErr)
	return s.ts.Reset(ctx, &st)
}

func (s *rpcStream) recvState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.recvDone {
		return io.EOF
	}
	return nil
}

func (s *rpcStream) markRecvDone() {
	s.mu.Lock()
	s.recvDone = true
	closeTransport := s.sendDone
	s.mu.Unlock()
	if closeTransport {
		_ = s.ts.Close(context.Background())
	}
}

func (s *rpcStream) markTerminal(err error) {
	if err == nil {
		err = status.Error(status.Unknown, "stream closed")
	}
	s.setTerminal(err)
	_ = s.ts.Close(context.Background())
}

func (s *rpcStream) setTerminal(err error) {
	if err == nil {
		err = status.Error(status.Unknown, "stream closed")
	}
	s.mu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
	}
	s.sendDone = true
	s.recvDone = true
	s.mu.Unlock()
}
