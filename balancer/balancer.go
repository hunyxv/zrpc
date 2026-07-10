package balancer

import (
	"context"
	"errors"

	"github.com/hunyxv/zrpc/transport"
)

var ErrNoEndpoints = errors.New("balancer: no endpoints")

type Balancer interface {
	Pick(ctx context.Context, endpoints []transport.Endpoint) (transport.Endpoint, error)
}

type pickFirst struct{}

func PickFirst() Balancer {
	return pickFirst{}
}

func (pickFirst) Pick(ctx context.Context, endpoints []transport.Endpoint) (transport.Endpoint, error) {
	if err := ctx.Err(); err != nil {
		return transport.Endpoint{}, err
	}
	if len(endpoints) == 0 {
		return transport.Endpoint{}, ErrNoEndpoints
	}
	return endpoints[0], nil
}
