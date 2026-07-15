package resolver

import (
	"context"

	"github.com/hunyxv/zrpc/transport"
)

// Update 表示 resolver 监听到的 endpoint 列表更新。
type Update struct {
	// Endpoints 是当前可用 endpoint 快照。
	Endpoints []transport.Endpoint
}

// Resolver 将用户目标解析成可连接的 endpoint 列表。
type Resolver interface {
	// Resolve 同步解析目标。
	Resolve(ctx context.Context, target string) ([]transport.Endpoint, error)
	// Watch 监听目标 endpoint 列表变化。
	Watch(ctx context.Context, target string) (<-chan Update, error)
}

// StaticResolver 始终返回固定 endpoint。
type StaticResolver struct {
	// Endpoint 是固定返回的 endpoint。
	Endpoint transport.Endpoint
}

// Static 创建固定 endpoint resolver。
func Static(endpoint transport.Endpoint) Resolver {
	return StaticResolver{Endpoint: endpoint}
}

// Resolve 返回固定 endpoint。
func (r StaticResolver) Resolve(ctx context.Context, target string) ([]transport.Endpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []transport.Endpoint{r.Endpoint}, nil
}

// Watch 返回一次固定 endpoint 更新后关闭 channel。
func (r StaticResolver) Watch(ctx context.Context, target string) (<-chan Update, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan Update, 1)
	ch <- Update{Endpoints: []transport.Endpoint{r.Endpoint}}
	close(ch)
	return ch, nil
}
