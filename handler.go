package zrpc

import "context"

type UnaryHandler interface {
	HandleUnary(ctx context.Context, req *Request) (*Response, error)
}

type UnaryHandlerFunc func(ctx context.Context, req *Request) (*Response, error)

func (f UnaryHandlerFunc) HandleUnary(ctx context.Context, req *Request) (*Response, error) {
	return f(ctx, req)
}

type StreamHandler interface {
	HandleStream(ctx context.Context, stream Stream) error
}

type StreamHandlerFunc func(ctx context.Context, stream Stream) error

func (f StreamHandlerFunc) HandleStream(ctx context.Context, stream Stream) error {
	return f(ctx, stream)
}
