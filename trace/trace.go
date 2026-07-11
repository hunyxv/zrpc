package trace

import (
	"context"

	"github.com/hunyxv/zrpc/metadata"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type carrier struct {
	md metadata.MD
}

func (c carrier) Get(key string) string {
	return c.md.Get(key)
}

func (c carrier) Set(key, value string) {
	c.md.Set(key, value)
}

func (c carrier) Keys() []string {
	keys := make([]string, 0, len(c.md))
	for key := range c.md {
		keys = append(keys, key)
	}
	return keys
}

func Inject(ctx context.Context, md metadata.MD) {
	otel.GetTextMapPropagator().Inject(ctx, carrier{md: md})
}

func Extract(ctx context.Context, md metadata.MD) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, carrier{md: md})
}

func Propagator() propagation.TextMapPropagator {
	return otel.GetTextMapPropagator()
}
