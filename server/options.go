package server

import (
	"time"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/transport"
)

type Options struct {
	Transport transport.Transport
	Endpoint  transport.Endpoint
	Codec     codec.Codec

	MaxConcurrentStreams    int
	MaxMessageSize          int
	InitialStreamWindow     int
	MaxConnInFlightBytes    int
	GracefulShutdownTimeout time.Duration
}
