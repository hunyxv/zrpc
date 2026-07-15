package interceptor

import (
	"context"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/protocol"
)

// UnaryInterceptor 包装请求-响应调用，可用于认证、日志、审计等横切逻辑。
type UnaryInterceptor func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error)

// StreamInterceptor 包装流式调用。
type StreamInterceptor func(ctx context.Context, stream zrpc.Stream, next zrpc.StreamHandler) error

// MessageHook 是预留的 per-message hook，可用于帧级观测或策略扩展。
type MessageHook interface {
	// OnSend 在发送 frame 前调用。
	OnSend(ctx context.Context, frame *protocol.Frame) error
	// OnRecv 在接收 frame 后调用。
	OnRecv(ctx context.Context, frame *protocol.Frame) error
}

// ChainUnary 按传入顺序组合 unary interceptor。
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

// ChainStream 按传入顺序组合 stream interceptor。
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
