package zrpc

import (
	"context"
)

// Context 上下文信息
//   思路：几个固定字段作为上下文信息，比如超时时间、环境变量、链路追踪等
type Context struct {
	context.Context
	// Key  string `msgpack:"key"`
}

func NewContext(ctx context.Context) context.Context {
	return Context{
		Context: ctx,
	}
}

// func (ctx *Context) MarshalMsgpack()([]byte, error){
// 	return msgpack.Marshal(&ctx)
// }

func (ctx *Context) UnmarshalMsgpack(b []byte) error {
	//msgpack.Unmarshal(b, &ctx)
	ctx.Context = context.Background()
	return nil
}
