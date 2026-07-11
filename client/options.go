package client

import (
	"time"

	"github.com/hunyxv/zrpc/balancer"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/resolver"
	"github.com/hunyxv/zrpc/transport"
)

type Options struct {
	Transport transport.Transport
	Target    transport.Endpoint
	Resolver  resolver.Resolver
	Balancer  balancer.Balancer
	Codec     codec.Codec
	Metrics   metrics.Collector

	DefaultTimeout       time.Duration
	MaxMessageSize       int
	InitialStreamWindow  int
	MaxConnInFlightBytes int
}
