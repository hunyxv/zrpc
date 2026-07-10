package zrpc

import (
	"context"
	"testing"
)

func TestUnaryHandlerFuncCallsFunction(t *testing.T) {
	called := false
	handler := UnaryHandlerFunc(func(ctx context.Context, req *Request) (*Response, error) {
		called = true
		if req.Method != "user.Get" {
			t.Fatalf("req.Method = %q", req.Method)
		}
		return &Response{}, nil
	})

	_, err := handler.HandleUnary(context.Background(), &Request{Method: "user.Get"})
	if err != nil {
		t.Fatalf("HandleUnary() error = %v", err)
	}
	if !called {
		t.Fatal("handler function was not called")
	}
}

func TestStreamHandlerFuncCallsFunction(t *testing.T) {
	called := false
	handler := StreamHandlerFunc(func(ctx context.Context, stream Stream) error {
		called = true
		if stream != nil {
			t.Fatalf("stream = %#v, want nil", stream)
		}
		return nil
	})

	if err := handler.HandleStream(context.Background(), nil); err != nil {
		t.Fatalf("HandleStream() error = %v", err)
	}
	if !called {
		t.Fatal("handler function was not called")
	}
}
