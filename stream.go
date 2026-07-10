package zrpc

import (
	"context"

	"github.com/hunyxv/zrpc/metadata"
)

type Stream interface {
	Context() context.Context
	Method() string
	Metadata() metadata.MD
	Send(ctx context.Context, msg any) error
	Recv(ctx context.Context, msg any) error
	CloseSend(ctx context.Context) error
	CloseRecv(ctx context.Context) error
	Reset(ctx context.Context, err error) error
}
