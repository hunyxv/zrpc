package interceptor

import (
	"context"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/protocol"
)

type UnaryInterceptor func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error)

type StreamInterceptor func(ctx context.Context, stream zrpc.Stream, next zrpc.StreamHandler) error

type MessageHook interface {
	OnSend(ctx context.Context, frame *protocol.Frame) error
	OnRecv(ctx context.Context, frame *protocol.Frame) error
}

func ChainUnary(items ...UnaryInterceptor) func(zrpc.UnaryHandler) zrpc.UnaryHandler {
	return func(final zrpc.UnaryHandler) zrpc.UnaryHandler {
		handler := final
		for i := len(items) - 1; i >= 0; i-- {
			current := items[i]
			next := handler
			handler = zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
				return current(ctx, req, next)
			})
		}
		return handler
	}
}

func ChainStream(items ...StreamInterceptor) func(zrpc.StreamHandler) zrpc.StreamHandler {
	return func(final zrpc.StreamHandler) zrpc.StreamHandler {
		handler := final
		for i := len(items) - 1; i >= 0; i-- {
			current := items[i]
			next := handler
			handler = zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
				return current(ctx, stream, next)
			})
		}
		return handler
	}
}
