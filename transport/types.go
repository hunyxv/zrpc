package transport

import (
	"context"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
)

type Endpoint struct {
	Transport string
	Address   string
}

type Transport interface {
	Dial(ctx context.Context, endpoint Endpoint, opts DialOptions) (Conn, error)
	Listen(endpoint Endpoint, opts ListenOptions) (Listener, error)
	Name() string
}

type Listener interface {
	Accept(ctx context.Context) (Conn, error)
	Close(ctx context.Context) error
}

type Conn interface {
	ID() string
	LocalEndpoint() Endpoint
	RemoteEndpoint() Endpoint
	OpenStream(ctx context.Context, method string, md metadata.MD) (TransportStream, error)
	AcceptStream(ctx context.Context) (TransportStream, error)
	Close(ctx context.Context) error
	Drain(ctx context.Context) error
}

type TransportStream interface {
	ID() string
	SendFrame(ctx context.Context, frame *protocol.Frame) error
	RecvFrame(ctx context.Context) (*protocol.Frame, error)
	Close(ctx context.Context) error
	Reset(ctx context.Context, st *status.Status) error
}
