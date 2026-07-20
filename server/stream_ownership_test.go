package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
)

func TestAcceptedTransportStreamClosedOnceAfterHandlerReturns(t *testing.T) {
	for _, tt := range []struct {
		name       string
		streamMode bool
		handlerErr error
	}{
		{name: "unary success"},
		{name: "unary failure", handlerErr: errors.New("unary failed")},
		{name: "stream success", streamMode: true},
		{name: "stream failure", streamMode: true, handlerErr: errors.New("stream failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const method = "ownership.Test"
			var handlerReturned atomic.Bool
			srv := New(Options{Codec: codec.Msgpack()})
			if tt.streamMode {
				srv.HandleStream(method, zrpc.StreamHandlerFunc(func(context.Context, zrpc.Stream) error {
					defer handlerReturned.Store(true)
					return tt.handlerErr
				}))
			} else {
				srv.HandleUnary(method, zrpc.UnaryHandlerFunc(func(context.Context, *zrpc.Request) (*zrpc.Response, error) {
					defer handlerReturned.Store(true)
					if tt.handlerErr != nil {
						return nil, tt.handlerErr
					}
					return zrpc.NewResponse(struct{}{}, codec.Msgpack())
				}))
			}

			stream := newOwnershipTestStream(t, method, tt.streamMode, &handlerReturned)
			srv.serveAcceptedStream(context.Background(), stream)

			if got := stream.closeCalls.Load(); got != 1 {
				t.Fatalf("Close() calls = %d, want 1", got)
			}
			if stream.closedBeforeHandler.Load() {
				t.Fatal("Close() called before handler returned")
			}
		})
	}
}

type ownershipTestStream struct {
	id                  string
	frames              chan *protocol.Frame
	closed              chan struct{}
	closeOnce           sync.Once
	closeCalls          atomic.Int32
	closedBeforeHandler atomic.Bool
	handlerReturned     *atomic.Bool
}

func newOwnershipTestStream(t *testing.T, method string, streamMode bool, handlerReturned *atomic.Bool) *ownershipTestStream {
	t.Helper()
	md := metadata.MD{}
	md.Set("method", method)
	if streamMode {
		md.Set("rpc-mode", "stream")
	}
	frames := make(chan *protocol.Frame, 2)
	frames <- &protocol.Frame{Type: protocol.FrameRequest, StreamID: "stream-1", Metadata: md}
	if !streamMode {
		body, err := codec.Msgpack().Marshal(struct{}{})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		frames <- &protocol.Frame{Type: protocol.FrameData, StreamID: "stream-1", Payload: body}
	}
	return &ownershipTestStream{
		id:              "stream-1",
		frames:          frames,
		closed:          make(chan struct{}),
		handlerReturned: handlerReturned,
	}
}

func (s *ownershipTestStream) ID() string { return s.id }

func (s *ownershipTestStream) SendFrame(context.Context, *protocol.Frame) error { return nil }

func (s *ownershipTestStream) RecvFrame(ctx context.Context) (*protocol.Frame, error) {
	select {
	case frame := <-s.frames:
		return frame, nil
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		return nil, errors.New("test stream closed")
	}
}

func (s *ownershipTestStream) Close(context.Context) error {
	s.closeCalls.Add(1)
	if !s.handlerReturned.Load() {
		s.closedBeforeHandler.Store(true)
	}
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *ownershipTestStream) Reset(context.Context, *status.Status) error { return nil }
