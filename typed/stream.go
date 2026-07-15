package typed

import (
	"context"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/server"
)

// ClientStream 表示客户端流式请求调用的客户端侧 stream。
type ClientStream[Req any, Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// ServerStream 表示客户端流式请求调用的服务端侧 stream。
type ServerStream[Req any, Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// ServerStreamingClient 表示流式响应调用的客户端侧接收器。
type ServerStreamingClient[Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// ServerSender 表示流式响应调用的服务端侧发送器。
type ServerSender[Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// BidiClientStream 表示双向流式调用的客户端侧 stream。
type BidiClientStream[Req any, Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// BidiServerStream 表示双向流式调用的服务端侧 stream。
type BidiServerStream[Req any, Resp any] struct {
	// Stream 是底层 zrpc stream，暴露给反射注册适配器使用。
	Stream zrpc.Stream
}

// NewClientStream 打开客户端流式请求调用。
func NewClientStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string) (*ClientStream[Req, Resp], error) {
	stream, err := cli.NewStream(ctx, method)
	if err != nil {
		return nil, err
	}
	return &ClientStream[Req, Resp]{Stream: stream}, nil
}

// Send 发送一条客户端流式请求消息。
func (s *ClientStream[Req, Resp]) Send(ctx context.Context, req *Req) error {
	return s.Stream.Send(ctx, req)
}

// CloseAndRecv 关闭请求发送方向并接收最终响应。
func (s *ClientStream[Req, Resp]) CloseAndRecv(ctx context.Context) (*Resp, error) {
	if err := s.Stream.CloseSend(ctx); err != nil {
		return nil, err
	}
	return recv[Resp](ctx, s.Stream)
}

// HandleClientStream 注册客户端流式请求 handler。
func HandleClientStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *ServerStream[Req, Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		return handler(ctx, &ServerStream[Req, Resp]{Stream: stream})
	}))
}

// Recv 接收一条客户端流式请求消息。
func (s *ServerStream[Req, Resp]) Recv(ctx context.Context) (*Req, error) {
	return recv[Req](ctx, s.Stream)
}

// SendAndClose 发送最终响应并关闭服务端发送方向。
func (s *ServerStream[Req, Resp]) SendAndClose(ctx context.Context, resp *Resp) error {
	if err := s.Stream.Send(ctx, resp); err != nil {
		return err
	}
	return s.Stream.CloseSend(ctx)
}

// NewServerStream 打开流式响应调用，并发送初始请求。
func NewServerStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string, req *Req) (*ServerStreamingClient[Resp], error) {
	stream, err := cli.NewStream(ctx, method)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(ctx, req); err != nil {
		_ = stream.Reset(ctx, err)
		return nil, err
	}
	if err := stream.CloseSend(ctx); err != nil {
		return nil, err
	}
	return &ServerStreamingClient[Resp]{Stream: stream}, nil
}

// Recv 接收一条服务端流式响应消息。
func (s *ServerStreamingClient[Resp]) Recv(ctx context.Context) (*Resp, error) {
	return recv[Resp](ctx, s.Stream)
}

// CloseRecv 关闭客户端接收方向。
func (s *ServerStreamingClient[Resp]) CloseRecv(ctx context.Context) error {
	return s.Stream.CloseRecv(ctx)
}

// HandleServerStream 注册流式响应 handler。
func HandleServerStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *Req, *ServerSender[Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		req, err := recv[Req](ctx, stream)
		if err != nil {
			return err
		}
		return handler(ctx, req, &ServerSender[Resp]{Stream: stream})
	}))
}

// Send 发送一条服务端流式响应消息。
func (s *ServerSender[Resp]) Send(ctx context.Context, resp *Resp) error {
	return s.Stream.Send(ctx, resp)
}

// NewBidiStream 打开双向流式调用。
func NewBidiStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string) (*BidiClientStream[Req, Resp], error) {
	stream, err := cli.NewStream(ctx, method)
	if err != nil {
		return nil, err
	}
	return &BidiClientStream[Req, Resp]{Stream: stream}, nil
}

// Send 发送一条双向流式请求消息。
func (s *BidiClientStream[Req, Resp]) Send(ctx context.Context, req *Req) error {
	return s.Stream.Send(ctx, req)
}

// Recv 接收一条双向流式响应消息。
func (s *BidiClientStream[Req, Resp]) Recv(ctx context.Context) (*Resp, error) {
	return recv[Resp](ctx, s.Stream)
}

// CloseSend 关闭客户端发送方向。
func (s *BidiClientStream[Req, Resp]) CloseSend(ctx context.Context) error {
	return s.Stream.CloseSend(ctx)
}

// CloseRecv 关闭客户端接收方向。
func (s *BidiClientStream[Req, Resp]) CloseRecv(ctx context.Context) error {
	return s.Stream.CloseRecv(ctx)
}

// HandleBidiStream 注册双向流式 handler。
func HandleBidiStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *BidiServerStream[Req, Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		return handler(ctx, &BidiServerStream[Req, Resp]{Stream: stream})
	}))
}

// Send 发送一条双向流式响应消息。
func (s *BidiServerStream[Req, Resp]) Send(ctx context.Context, resp *Resp) error {
	return s.Stream.Send(ctx, resp)
}

// Recv 接收一条双向流式请求消息。
func (s *BidiServerStream[Req, Resp]) Recv(ctx context.Context) (*Req, error) {
	return recv[Req](ctx, s.Stream)
}

func recv[T any](ctx context.Context, stream zrpc.Stream) (*T, error) {
	var out T
	if err := stream.Recv(ctx, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
