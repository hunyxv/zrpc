package zrpc

import (
	"context"
	"log"

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
)

// Context 上下文信息
//   思路：几个固定字段作为上下文信息，比如超时时间、环境变量、链路追踪等
type Context struct {
	context.Context
	//Payload  map `msgpack:"payload" json:"payload"`
}

func NewContext(ctx context.Context) *Context {
	return &Context{
		Context: ctx,
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

	return msgpack.Marshal(payload)
}

func (ctx *Context) UnmarshalMsgpack(b []byte) error {
	if ctx.Context == nil {
		ctx.Context = context.Background()
	}

	if len(b) > 0 {
		var m map[zrpcContextKey]map[string]interface{}
		if err := msgpack.Unmarshal(b, &m); err != nil {
			return err
		}

		if p, ok := m[TracePayloadKey]; ok {
			payload := make(map[string]string, len(p))
			for k, v := range p {
				if s, ok := v.(string); ok {
					payload[k] = s
				}
			}
			ctx.Context = context.WithValue(ctx.Context, TracePayloadKey, payload)
		}

		if v, ok := m[PayloadKey]; ok {
			ctx.Context = context.WithValue(ctx.Context, PayloadKey, v)
		}
	}
	return nil
}

// InjectTrace2ctx 提取链路追中上下文信息，并注入到新的 context 中
func InjectTrace2ctx(ctx context.Context) context.Context {
	t := ctx.Value(TracePayloadKey)
	var payload map[string]string
	if !isNil(t) {
		if data, ok := t.(map[string]string); ok {
			payload = data
		} else {
			payload = make(map[string]string)
		}
	} else {
		payload = map[string]string{}
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(payload))
	log.Println(payload)

	valueCtx := context.WithValue(ctx, TracePayloadKey, payload)
	return valueCtx
}
