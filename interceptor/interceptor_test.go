package interceptor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/protocol"
)

func TestUnaryChainOrder(t *testing.T) {
	calls := []string{}
	first := func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error) {
		calls = append(calls, "first-before")
		resp, err := next.HandleUnary(ctx, req)
		calls = append(calls, "first-after")
		return resp, err
	}
	second := func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error) {
		calls = append(calls, "second-before")
		resp, err := next.HandleUnary(ctx, req)
		calls = append(calls, "second-after")
		return resp, err
	}
	final := zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		calls = append(calls, "handler")
		return &zrpc.Response{}, nil
	})

	_, err := ChainUnary(first, second)(final).HandleUnary(context.Background(), &zrpc.Request{})
	if err != nil {
		t.Fatalf("HandleUnary() error = %v", err)
	}
	want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v want %#v", calls, want)
	}
}

func TestUnaryChainShortCircuit(t *testing.T) {
	errBlocked := errors.New("blocked")
	called := false
	blocking := func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error) {
		return nil, errBlocked
	}
	final := zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		called = true
		return &zrpc.Response{}, nil
	})

	_, err := ChainUnary(blocking)(final).HandleUnary(context.Background(), &zrpc.Request{})
	if !errors.Is(err, errBlocked) {
		t.Fatalf("HandleUnary() error = %v, want %v", err, errBlocked)
	}
	if called {
		t.Fatal("final handler was called after interceptor short-circuit")
	}
}

func TestStreamChainOrder(t *testing.T) {
	calls := []string{}
	first := func(ctx context.Context, stream zrpc.Stream, next zrpc.StreamHandler) error {
		calls = append(calls, "first-before")
		err := next.HandleStream(ctx, stream)
		calls = append(calls, "first-after")
		return err
	}
	second := func(ctx context.Context, stream zrpc.Stream, next zrpc.StreamHandler) error {
		calls = append(calls, "second-before")
		err := next.HandleStream(ctx, stream)
		calls = append(calls, "second-after")
		return err
	}
	final := zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		calls = append(calls, "handler")
		return nil
	})

	if err := ChainStream(first, second)(final).HandleStream(context.Background(), nil); err != nil {
		t.Fatalf("HandleStream() error = %v", err)
	}
	want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v want %#v", calls, want)
	}
}

func TestMessageHookShape(t *testing.T) {
	var hook MessageHook = messageHookFunc{
		send: func(ctx context.Context, frame *protocol.Frame) error { return nil },
		recv: func(ctx context.Context, frame *protocol.Frame) error { return nil },
	}
	if err := hook.OnSend(context.Background(), &protocol.Frame{Type: protocol.FramePing}); err != nil {
		t.Fatalf("OnSend() error = %v", err)
	}
	if err := hook.OnRecv(context.Background(), &protocol.Frame{Type: protocol.FramePing}); err != nil {
		t.Fatalf("OnRecv() error = %v", err)
	}
}

type messageHookFunc struct {
	send func(context.Context, *protocol.Frame) error
	recv func(context.Context, *protocol.Frame) error
}

func (h messageHookFunc) OnSend(ctx context.Context, frame *protocol.Frame) error {
	return h.send(ctx, frame)
}

func (h messageHookFunc) OnRecv(ctx context.Context, frame *protocol.Frame) error {
	return h.recv(ctx, frame)
}
