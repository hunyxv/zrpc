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

type staticResolver struct {
	endpoint transport.Endpoint
}

func Static(endpoint transport.Endpoint) Resolver {
	return staticResolver{endpoint: endpoint}
}

func (r staticResolver) Resolve(ctx context.Context, target string) ([]transport.Endpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []transport.Endpoint{r.endpoint}, nil
}

func (r staticResolver) Watch(ctx context.Context, target string) (<-chan Update, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan Update, 1)
	ch <- Update{Endpoints: []transport.Endpoint{r.endpoint}}
	close(ch)
	return ch, nil
}
