package typed

import (
	"context"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/server"
)

type ClientStream[Req any, Resp any] struct {
	Stream zrpc.Stream
}

type ServerStream[Req any, Resp any] struct {
	Stream zrpc.Stream
}

type ServerStreamingClient[Resp any] struct {
	Stream zrpc.Stream
}

type ServerSender[Resp any] struct {
	Stream zrpc.Stream
}

type BidiClientStream[Req any, Resp any] struct {
	Stream zrpc.Stream
}

type BidiServerStream[Req any, Resp any] struct {
	Stream zrpc.Stream
}

func NewClientStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string) (*ClientStream[Req, Resp], error) {
	stream, err := cli.NewStream(ctx, method)
	if err != nil {
		return nil, err
	}
	return &ClientStream[Req, Resp]{Stream: stream}, nil
}

func (s *ClientStream[Req, Resp]) Send(ctx context.Context, req *Req) error {
	return s.Stream.Send(ctx, req)
}

func (s *ClientStream[Req, Resp]) CloseAndRecv(ctx context.Context) (*Resp, error) {
	if err := s.Stream.CloseSend(ctx); err != nil {
		return nil, err
	}
	return recv[Resp](ctx, s.Stream)
}

func HandleClientStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *ServerStream[Req, Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		return handler(ctx, &ServerStream[Req, Resp]{Stream: stream})
	}))
}

func (s *ServerStream[Req, Resp]) Recv(ctx context.Context) (*Req, error) {
	return recv[Req](ctx, s.Stream)
}

func (s *ServerStream[Req, Resp]) SendAndClose(ctx context.Context, resp *Resp) error {
	if err := s.Stream.Send(ctx, resp); err != nil {
		return err
	}
	return s.Stream.CloseSend(ctx)
}

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

func (s *ServerStreamingClient[Resp]) Recv(ctx context.Context) (*Resp, error) {
	return recv[Resp](ctx, s.Stream)
}

func (s *ServerStreamingClient[Resp]) CloseRecv(ctx context.Context) error {
	return s.Stream.CloseRecv(ctx)
}

func HandleServerStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *Req, *ServerSender[Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		req, err := recv[Req](ctx, stream)
		if err != nil {
			return err
		}
		return handler(ctx, req, &ServerSender[Resp]{Stream: stream})
	}))
}

func (s *ServerSender[Resp]) Send(ctx context.Context, resp *Resp) error {
	return s.Stream.Send(ctx, resp)
}

func NewBidiStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string) (*BidiClientStream[Req, Resp], error) {
	stream, err := cli.NewStream(ctx, method)
	if err != nil {
		return nil, err
	}
	return &BidiClientStream[Req, Resp]{Stream: stream}, nil
}

func (s *BidiClientStream[Req, Resp]) Send(ctx context.Context, req *Req) error {
	return s.Stream.Send(ctx, req)
}

func (s *BidiClientStream[Req, Resp]) Recv(ctx context.Context) (*Resp, error) {
	return recv[Resp](ctx, s.Stream)
}

func (s *BidiClientStream[Req, Resp]) CloseSend(ctx context.Context) error {
	return s.Stream.CloseSend(ctx)
}

func (s *BidiClientStream[Req, Resp]) CloseRecv(ctx context.Context) error {
	return s.Stream.CloseRecv(ctx)
}

func HandleBidiStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *BidiServerStream[Req, Resp]) error) {
	srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		return handler(ctx, &BidiServerStream[Req, Resp]{Stream: stream})
	}))
}

func (s *BidiServerStream[Req, Resp]) Send(ctx context.Context, resp *Resp) error {
	return s.Stream.Send(ctx, resp)
}

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
