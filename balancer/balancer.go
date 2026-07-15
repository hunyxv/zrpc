package balancer

import (
	"context"
	"errors"

	"github.com/hunyxv/zrpc/transport"
)

// ErrNoEndpoints 表示没有可选 endpoint。
var ErrNoEndpoints = errors.New("balancer: no endpoints")

// Balancer 从 resolver 返回的 endpoint 列表中选择一个目标。
type Balancer interface {
	// Pick 返回本次调用应使用的 endpoint。
	Pick(ctx context.Context, endpoints []transport.Endpoint) (transport.Endpoint, error)
}

type pickFirst struct{}

// PickFirst 返回始终选择第一个 endpoint 的 Balancer。
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
