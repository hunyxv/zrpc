package client

import (
	"time"

	"github.com/hunyxv/zrpc"
)

type ServerInfo struct {
	ServerName    string
	NodeID        string
	LocalEndpoint zrpc.Endpoint // 本地 endpoint
	StateEndpoint zrpc.Endpoint // 状态 endpoint
}

type Option func(opt *options)

type options struct {
	// MinIdleConns number of idle connection.
	MinIdleConns int
	// PoolSize Maximum number of socket connections.
	PoolSize int
	// ReuseCount upper limit for reuse of one connections.
	ReuseCount int32
	// Type of connection pool. true for FIFO pool, false for LIFO pool.
	// Note that fifo has higher overhead compared to lifo.
	PoolFIFO bool
	// Amount of time client waits for connection if all connections
	// are busy before returning an error.
	// Default is 3 second.
	PoolTimeout time.Duration
	// IdleTimeout amount of time after which client closes idle connections.
	// Default is 5 minutes.
	IdleTimeout time.Duration
	// 
	IdleCheckFrequency time.Duration
	// Connection age at which client retires (closes) the connection.
	// Default is to not close aged connections.
	MaxConnAge time.Duration
	// 
	OnClose func(*ZrpcClient) error
}

func WithPoolSize(size int) Option {
	return func(opt *options) {
		if size == 0 {
			size = 1
		}

		opt.PoolSize = size
	}
}

func WithMinIdleCount(n int) Option {
	return func(opt *options) {
		if n == 0 {
			n = 1
		}

		opt.MinIdleConns = n
	}
}
