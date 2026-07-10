package balancer

import (
	"context"
	"errors"
	"testing"

	"github.com/hunyxv/zrpc/transport"
)

func TestPickFirst(t *testing.T) {
	b := PickFirst()
	endpoints := []transport.Endpoint{
		{Transport: "fake", Address: "one"},
		{Transport: "fake", Address: "two"},
	}

	got, err := b.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got != endpoints[0] {
		t.Fatalf("endpoint = %#v, want %#v", got, endpoints[0])
	}
}

func TestPickFirstRejectsEmptyEndpoints(t *testing.T) {
	_, err := PickFirst().Pick(context.Background(), nil)
	if !errors.Is(err, ErrNoEndpoints) {
		t.Fatalf("Pick() error = %v, want %v", err, ErrNoEndpoints)
	}
}

func TestPickFirstHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := PickFirst().Pick(ctx, []transport.Endpoint{{Transport: "fake", Address: "svc"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Pick() error = %v, want %v", err, context.Canceled)
	}
}
