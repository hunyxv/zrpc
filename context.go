package zrpc

import (
	"context"

	"github.com/vmihailenco/msgpack/v5"
)

type contextKey string

// Context 上下文信息
//   思路：几个固定字段作为上下文信息，比如超时时间、环境变量、链路追踪等
type Context struct {
	context.Context
	// Key  string `msgpack:"key"`
}

func NewContext(ctx context.Context) *Context {
	return &Context{
		Context: ctx,
	}
}

// func (ctx *Context) MarshalMsgpack()([]byte, error){
// 	return msgpack.Marshal(&ctx)
// }

func (ctx *Context) UnmarshalMsgpack(b []byte) error {
	//msgpack.Unmarshal(b, &ctx)
	var m map[string]interface{} 
	msgpack.Unmarshal(b, &m)
	ctx.Context = context.WithValue(ctx.Context, contextKey("_key_"), m)
	return nil
}

func (ctx *Context) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}

func (ctx *Context)  UnmarshalJSON(b []byte) error {
	var m map[string]interface{} 
	msgpack.Unmarshal(b, &m)
	ctx.Context = context.WithValue(ctx.Context, contextKey("_key_"), m)
	return nil
}