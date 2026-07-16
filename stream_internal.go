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

// defaultStreamWindow 是未显式配置时的单 stream 初始窗口大小。
const defaultStreamWindow = 1024 * 1024

// defaultStreamRecvQueueSize 是 pump 投递给业务 Recv 的默认 data frame 队列长度。
const defaultStreamRecvQueueSize = 1024

type rpcStream struct {
	ctx    context.Context
	method string
	md     metadata.MD
	codec  codec.Codec
	ts     transport.TransportStream
	dir    protocol.Direction

	sendWin *protocol.Window
	recvWin *protocol.Window

	mu            sync.Mutex
	sendDone      bool
	recvDone      bool
	recvEnd       bool
	recvFrames    []*protocol.Frame
	changed       chan struct{}
	terminalErr   error
	pumpCancel    context.CancelFunc
	pumpStopping  bool
	recvQueueSize int
}

// NewInternalStream 将 transport stream 包装为面向业务消息的 Stream。
func NewInternalStream(ctx context.Context, method string, md metadata.MD, c codec.Codec, ts transport.TransportStream, window int, direction ...protocol.Direction) Stream {
	if window <= 0 {
		window = defaultStreamWindow
	}
	dir := protocol.DirectionNone
	if len(direction) > 0 {
		dir = direction[0]
	}
	pumpCtx, cancel := context.WithCancel(ctx)
	stream := &rpcStream{
		ctx:     ctx,
		method:  method,
		md:      md.Copy(),
		codec:   c,
		ts:      ts,
		dir:     dir,
		sendWin: protocol.NewWindow(window),
		recvWin: protocol.NewWindow(window),
		changed: make(chan struct{}),

		pumpCancel:    cancel,
		recvQueueSize: defaultStreamRecvQueueSize,
	}
	go stream.pump(pumpCtx)
	return stream
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		_ = s.sendWin.Release(len(raw))
		return s.terminalErr
	}
	if s.sendDone {
		_ = s.sendWin.Release(len(raw))
		return status.Error(status.FailedPrecondition, "stream send side closed")
	}
	if err := s.ts.SendFrame(ctx, &protocol.Frame{
		Type:      protocol.FrameData,
		StreamID:  s.ts.ID(),
		Direction: s.dir,
		Payload:   raw,
	}); err != nil {
		_ = s.sendWin.Release(len(raw))
		return err
	}
	return nil
}

func (s *rpcStream) Recv(ctx context.Context, msg any) error {
	frame, err := s.nextRecvFrame(ctx)
	if err != nil {
		return err
	}
	decodeErr := s.codec.Unmarshal(frame.Payload, msg)
	updateErr := s.sendWindowUpdate(ctx, len(frame.Payload), frame.Direction)
	if decodeErr != nil {
		return decodeErr
	}
	return updateErr
}

func (s *rpcStream) pump(ctx context.Context) {
	for {
		frame, err := s.ts.RecvFrame(ctx)
		if err != nil {
			s.markPumpError(err)
			return
		}
		if err := s.handlePumpFrame(frame); err != nil {
			s.setTerminal(err)
			_ = s.ts.Reset(context.Background(), statusPtr(err))
			return
		}
	}
}

func (s *rpcStream) handlePumpFrame(frame *protocol.Frame) error {
	if frame == nil {
		return status.Error(status.Internal, "nil stream frame")
	}
	switch frame.Type {
	case protocol.FrameData:
		return s.enqueueRecvFrame(frame)
	case protocol.FrameWindowUpdate:
		return s.sendWin.Release(frame.Window)
	case protocol.FrameEnd:
		s.markRecvEnd()
		return nil
	case protocol.FrameReset:
		err := status.Error(status.Unknown, "stream reset")
		if frame.Status != nil {
			err = status.WithDetails(status.Error(frame.Status.Code, frame.Status.Message), frame.Status.Details...)
		}
		s.markTerminal(err)
		return nil
	default:
		return status.Error(status.Internal, "unexpected stream frame")
	}
}

func (s *rpcStream) CloseSend(ctx context.Context) error {
	s.mu.Lock()
	if s.terminalErr != nil {
		s.mu.Unlock()
		return s.terminalErr
	}
	if s.sendDone {
		s.mu.Unlock()
		return nil
	}
	s.sendDone = true
	err := s.ts.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameEnd, StreamID: s.ts.ID(), Direction: s.dir})
	stopPump := s.recvDone
	s.mu.Unlock()
	if stopPump {
		s.stopPump()
	}
	return err
}

func (s *rpcStream) CloseRecv(ctx context.Context) error {
	s.mu.Lock()
	s.recvDone = true
	s.recvFrames = nil
	closeTransport := s.sendDone
	s.signalLocked()
	s.mu.Unlock()
	if closeTransport {
		s.stopPump()
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

func (s *rpcStream) nextRecvFrame(ctx context.Context) (*protocol.Frame, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.terminalErr != nil {
			err := s.terminalErr
			s.mu.Unlock()
			return nil, err
		}
		if len(s.recvFrames) > 0 {
			frame := s.recvFrames[0]
			s.recvFrames[0] = nil
			s.recvFrames = s.recvFrames[1:]
			s.mu.Unlock()
			return frame, nil
		}
		if s.recvDone || s.recvEnd {
			s.recvDone = true
			closeTransport := s.sendDone
			s.signalLocked()
			s.mu.Unlock()
			if closeTransport {
				s.stopPump()
				_ = s.ts.Close(context.Background())
			}
			return nil, io.EOF
		}
		changed := s.changed
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
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
	s.recvFrames = nil
	s.signalLocked()
	s.mu.Unlock()
	s.sendWin.ReleaseAll()
	s.stopPump()
}

func (s *rpcStream) enqueueRecvFrame(frame *protocol.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.recvDone {
		return status.Error(status.FailedPrecondition, "stream receive side closed")
	}
	if s.recvEnd {
		return status.Error(status.Internal, "stream data after end")
	}
	if len(s.recvFrames) >= s.recvQueueSize {
		return status.Error(status.ResourceExhausted, "stream receive queue full")
	}
	cloned := *frame
	if frame.Payload != nil {
		cloned.Payload = append([]byte(nil), frame.Payload...)
	}
	if frame.Metadata != nil {
		cloned.Metadata = frame.Metadata.Copy()
	}
	if frame.Status != nil {
		st := *frame.Status
		if frame.Status.Details != nil {
			st.Details = append([]string(nil), frame.Status.Details...)
		}
		cloned.Status = &st
	}
	s.recvFrames = append(s.recvFrames, &cloned)
	s.signalLocked()
	return nil
}

func (s *rpcStream) markRecvEnd() {
	s.mu.Lock()
	if s.terminalErr == nil {
		s.recvEnd = true
		s.signalLocked()
	}
	s.mu.Unlock()
}

func (s *rpcStream) markPumpError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	ignore := s.pumpStopping || (s.sendDone && s.recvDone)
	if !ignore && s.terminalErr == nil {
		s.terminalErr = err
		s.sendDone = true
		s.recvDone = true
		s.recvFrames = nil
		s.signalLocked()
	}
	s.mu.Unlock()
	if !ignore {
		s.sendWin.ReleaseAll()
	}
}

func (s *rpcStream) sendWindowUpdate(ctx context.Context, n int, dir protocol.Direction) error {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	return s.ts.SendFrame(ctx, &protocol.Frame{
		Type:      protocol.FrameWindowUpdate,
		StreamID:  s.ts.ID(),
		Direction: dir,
		Window:    n,
	})
}

func (s *rpcStream) stopPump() {
	s.mu.Lock()
	if !s.pumpStopping {
		s.pumpStopping = true
		s.pumpCancel()
	}
	s.mu.Unlock()
}

func (s *rpcStream) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func statusPtr(err error) *status.Status {
	st := status.FromError(err)
	return &st
}
