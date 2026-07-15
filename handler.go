package zrpc

import "context"

// UnaryHandler 处理一次请求-响应 RPC。
type UnaryHandler interface {
	HandleUnary(ctx context.Context, req *Request) (*Response, error)
}

// UnaryHandlerFunc 将函数适配为 UnaryHandler。
type UnaryHandlerFunc func(ctx context.Context, req *Request) (*Response, error)

// HandleUnary 调用底层函数。
func (f UnaryHandlerFunc) HandleUnary(ctx context.Context, req *Request) (*Response, error) {
	return f(ctx, req)
}

// StreamHandler 处理任意一种流式 RPC。
type StreamHandler interface {
	HandleStream(ctx context.Context, stream Stream) error
}

// StreamHandlerFunc 将函数适配为 StreamHandler。
type StreamHandlerFunc func(ctx context.Context, stream Stream) error

// HandleStream 调用底层函数。
func (f StreamHandlerFunc) HandleStream(ctx context.Context, stream Stream) error {
	return f(ctx, stream)
}
