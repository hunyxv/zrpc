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

	sendWin *protocol.Window
	recvWin *protocol.Window

	mu       sync.Mutex
	sendDone bool
	recvDone bool
}

func NewInternalStream(ctx context.Context, method string, md metadata.MD, c codec.Codec, ts transport.TransportStream, window int) Stream {
	if window <= 0 {
		window = defaultStreamWindow
	}
	return &rpcStream{
		ctx:     ctx,
		method:  method,
		md:      md.Copy(),
		codec:   c,
		ts:      ts,
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
	if err := s.ensureCanSend(); err != nil {
		return err
	}
	raw, err := s.codec.Marshal(msg)
	if err != nil {
		return err
	}
	if err := s.sendWin.Acquire(ctx, len(raw)); err != nil {
		return err
	}
	return s.ts.SendFrame(ctx, &protocol.Frame{
		Type:     protocol.FrameData,
		StreamID: s.ts.ID(),
		Payload:  raw,
	})
}

func (s *rpcStream) Recv(ctx context.Context, msg any) error {
	if err := s.ensureCanRecv(); err != nil {
		return err
	}
	frame, err := s.ts.RecvFrame(ctx)
	if err != nil {
		return err
	}
	switch frame.Type {
	case protocol.FrameData:
		s.recvWin.Release(len(frame.Payload))
		return s.codec.Unmarshal(frame.Payload, msg)
	case protocol.FrameEnd:
		s.mu.Lock()
		s.recvDone = true
		s.mu.Unlock()
		return io.EOF
	case protocol.FrameReset:
		if frame.Status != nil {
			return status.WithDetails(status.Error(frame.Status.Code, frame.Status.Message), frame.Status.Details...)
		}
		return status.Error(status.Unknown, "stream reset")
	default:
		return status.Error(status.Internal, "unexpected stream frame")
	}
}

func (s *rpcStream) CloseSend(ctx context.Context) error {
	s.mu.Lock()
	if s.sendDone {
		s.mu.Unlock()
		return nil
	}
	s.sendDone = true
	s.mu.Unlock()
	return s.ts.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameEnd, StreamID: s.ts.ID()})
}

func (s *rpcStream) CloseRecv(ctx context.Context) error {
	s.mu.Lock()
	s.recvDone = true
	s.mu.Unlock()
	return nil
}

func (s *rpcStream) Reset(ctx context.Context, err error) error {
	st := status.FromError(err)
	return s.ts.Reset(ctx, &st)
}

func (s *rpcStream) ensureCanSend() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendDone {
		return status.Error(status.FailedPrecondition, "stream send side closed")
	}
	return nil
}

func (s *rpcStream) ensureCanRecv() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recvDone {
		return io.EOF
	}
	return nil
}
