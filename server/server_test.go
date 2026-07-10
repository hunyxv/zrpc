package server

import (
	"context"
	"testing"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

func TestServeRequiresTransportAndCodec(t *testing.T) {
	if err := New(Options{Codec: codec.Msgpack()}).Serve(context.Background()); err == nil {
		t.Fatal("Serve() without transport error = nil, want non-nil")
	}
	if err := New(Options{Transport: fake.New(), Endpoint: transport.Endpoint{Transport: "fake", Address: "svc"}}).Serve(context.Background()); err == nil {
		t.Fatal("Serve() without codec error = nil, want non-nil")
	}
}
