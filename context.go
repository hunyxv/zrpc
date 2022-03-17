package zrpc

import (
	"context"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type zrpcContextKey string

const (
	// 关于链路追踪的数据
	TracePayloadKey zrpcContextKey = "__trace_ctx__"
	// 其他数据
	PayloadKey zrpcContextKey = "__ctx__"

	// ctx 截止时间
	DeadlineKey zrpcContextKey = "__deadline__"
)

// Context 上下文信息
//   思路：几个固定字段作为上下文信息，比如超时时间、环境变量、链路追踪等
type Context struct {
	context.Context
	cancel context.CancelFunc
}

func NewContext() *Context {
	return &Context{
		Context: context.Background(),
	}
}

func (ctx *Context) Cancel() {
	if ctx.cancel != nil {
		ctx.cancel()
	}
}

func (ctx *Context) MarshalMsgpack() ([]byte, error) {
	payload := make(map[zrpcContextKey]interface{}, 2)
	if data := ctx.Value(TracePayloadKey); !isNil(data) {
		payload[TracePayloadKey] = data
	}
	if data := ctx.Value(PayloadKey); !isNil(data) {
		payload[PayloadKey] = data
	}
	if deadline, ok := ctx.Deadline(); ok {
		payload[DeadlineKey] = deadline.UnixNano()
	}

	return msgpack.Marshal(payload)
}

func (ctx *Context) UnmarshalMsgpack(b []byte) error {
	if ctx.Context == nil {
		ctx.Context = context.Background()
	}

	if len(b) > 0 {
		var m map[zrpcContextKey]interface{}
		if err := msgpack.Unmarshal(b, &m); err != nil {
			return err
		}

		if v, ok := m[TracePayloadKey]; ok {
			ctx.Context = context.WithValue(ctx.Context, TracePayloadKey, v)
		}

		if v, ok := m[PayloadKey]; ok {
			ctx.Context = context.WithValue(ctx.Context, PayloadKey, v)
		}

		if v, ok := m[DeadlineKey]; ok {
			if deadline, ok := v.(int64); ok {
				timeout := time.Since(time.Unix(0, deadline))
				ctx.Context, ctx.cancel = context.WithTimeout(ctx.Context, timeout)
			}
		}
	}
	return nil
}

// InjectTrace2ctx 提取链路追中上下文信息，并注入到新的 context 中
func InjectTrace2ctx(ctx context.Context) context.Context {
	payload := map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(payload))
	if len(payload) == 0 {
		return ctx
	}
	valueCtx := context.WithValue(ctx, TracePayloadKey, payload)
	return valueCtx
}

func ExtractTraceid(ctx context.Context) (m map[string]string) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(m))
	return
}
