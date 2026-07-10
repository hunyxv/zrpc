package resolver

import (
	"context"

	"github.com/hunyxv/zrpc/transport"
)

type Update struct {
	Endpoints []transport.Endpoint
}

type Resolver interface {
	Resolve(ctx context.Context, target string) ([]transport.Endpoint, error)
	Watch(ctx context.Context, target string) (<-chan Update, error)
}

type StaticResolver struct {
	Endpoint transport.Endpoint
}

func Static(endpoint transport.Endpoint) Resolver {
	return StaticResolver{Endpoint: endpoint}
}

func (r StaticResolver) Resolve(ctx context.Context, target string) ([]transport.Endpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []transport.Endpoint{r.Endpoint}, nil
}

func (r StaticResolver) Watch(ctx context.Context, target string) (<-chan Update, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan Update, 1)
	ch <- Update{Endpoints: []transport.Endpoint{r.Endpoint}}
	close(ch)
	return ch, nil
}
