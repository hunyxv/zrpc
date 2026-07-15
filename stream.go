package zrpc

import (
	"context"

	"github.com/hunyxv/zrpc/metadata"
)

// Stream 表示一个双向可收发的 RPC stream。
type Stream interface {
	// Context 返回创建 stream 时绑定的上下文。
	Context() context.Context
	// Method 返回当前 RPC 方法名。
	Method() string
	// Metadata 返回 stream 初始 metadata 的副本。
	Metadata() metadata.MD
	// Send 发送一条业务消息。
	Send(ctx context.Context, msg any) error
	// Recv 接收一条业务消息并解码到 msg。
	Recv(ctx context.Context, msg any) error
	// CloseSend 关闭本端发送方向。
	CloseSend(ctx context.Context) error
	// CloseRecv 关闭本端接收方向。
	CloseRecv(ctx context.Context) error
	// Reset 以错误状态终止 stream。
	Reset(ctx context.Context, err error) error
}
