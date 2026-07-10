package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/balancer"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/resolver"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

type unaryReq struct {
	Name string `msgpack:"name"`
}

type unaryResp struct {
	Message string `msgpack:"message"`
}

func TestClientInvokeUnary(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleUnary("hello.Say", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		var in unaryReq
		if err := req.Decode(&in); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		return zrpc.NewResponse(unaryResp{Message: "hello " + in.Name}, codec.Msgpack())
	}))
	cancel := serveInBackground(t, srv)
	defer cancel()

	cli := waitForClient(t, Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() {
		if err := cli.Close(context.Background()); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	resp, err := cli.Invoke(context.Background(), "hello.Say", unaryReq{Name: "zrpc"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var out unaryResp
	if err := resp.Decode(&out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if out.Message != "hello zrpc" {
		t.Fatalf("message = %q", out.Message)
	}
}

func TestClientInvokeUnknownMethod(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	cancel := serveInBackground(t, srv)
	defer cancel()

	cli := waitForClient(t, Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()

	_, err := cli.Invoke(context.Background(), "missing.Method", unaryReq{Name: "zrpc"})
	st := status.FromError(err)
	if st.Code != status.Unimplemented {
		t.Fatalf("status code = %v, want %v (err=%v)", st.Code, status.Unimplemented, err)
	}
}

func TestClientInvokeHandlerError(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleUnary("hello.Fail", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		return nil, status.Error(status.PermissionDenied, "denied")
	}))
	cancel := serveInBackground(t, srv)
	defer cancel()

	cli := waitForClient(t, Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()

	_, err := cli.Invoke(context.Background(), "hello.Fail", unaryReq{Name: "zrpc"})
	st := status.FromError(err)
	if st.Code != status.PermissionDenied || st.Message != "denied" {
		t.Fatalf("status = %#v", st)
	}
}

func TestClientNewUsesResolverAndBalancer(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "resolved-svc"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleUnary("hello.Say", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		return zrpc.NewResponse(unaryResp{Message: "ok"}, codec.Msgpack())
	}))
	cancel := serveInBackground(t, srv)
	defer cancel()

	cli := waitForClient(t, Options{
		Transport: tr,
		Target:    transport.Endpoint{Transport: "fake", Address: "logical-name"},
		Resolver:  resolver.Static(endpoint),
		Balancer:  balancer.PickFirst(),
		Codec:     codec.Msgpack(),
	})
	defer func() { _ = cli.Close(context.Background()) }()

	resp, err := cli.Invoke(context.Background(), "hello.Say", unaryReq{Name: "zrpc"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var out unaryResp
	if err := resp.Decode(&out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if out.Message != "ok" {
		t.Fatalf("message = %q, want ok", out.Message)
	}
}

func serveInBackground(t *testing.T, srv *server.Server) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Serve() did not stop after context cancellation")
		}
	})
	return cancel
}

func waitForClient(t *testing.T, opts Options) *Client {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cli, err := New(opts)
		if err == nil {
			return cli
		}
		lastErr = err
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("New() did not connect: %v", lastErr)
	return nil
}
