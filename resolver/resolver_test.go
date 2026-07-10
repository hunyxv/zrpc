package resolver

import (
	"context"
	"testing"

	"github.com/hunyxv/zrpc/transport"
)

func TestStaticResolverResolve(t *testing.T) {
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	r := Static(endpoint)

	got, err := r.Resolve(context.Background(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != 1 || got[0] != endpoint {
		t.Fatalf("endpoints = %#v", got)
	}
}

func TestStaticResolverWatch(t *testing.T) {
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	r := Static(endpoint)

	updates, err := r.Watch(context.Background(), "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	update, ok := <-updates
	if !ok {
		t.Fatal("updates closed before initial update")
	}
	if len(update.Endpoints) != 1 || update.Endpoints[0] != endpoint {
		t.Fatalf("update.Endpoints = %#v", update.Endpoints)
	}
	if _, ok := <-updates; ok {
		t.Fatal("updates channel should be closed after static update")
	}
}

func TestStaticResolverHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Static(transport.Endpoint{Transport: "fake", Address: "svc"})

	if _, err := r.Resolve(ctx, "svc"); err == nil {
		t.Fatal("Resolve() error = nil, want non-nil")
	}
	if _, err := r.Watch(ctx, "svc"); err == nil {
		t.Fatal("Watch() error = nil, want non-nil")
	}
}
