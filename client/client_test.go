package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/balancer"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/protocol"
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

func TestClientInvokeRejectsUnexpectedResponseFrame(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close(context.Background()) }()
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		if _, err := stream.RecvFrame(context.Background()); err != nil {
			errCh <- err
			return
		}
		if _, err := stream.RecvFrame(context.Background()); err != nil {
			errCh <- err
			return
		}
		errCh <- stream.SendFrame(context.Background(), &protocol.Frame{
			Type:     protocol.FrameData,
			StreamID: stream.ID(),
			Payload:  []byte("not-a-response"),
		})
	}()

	cli, err := New(Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = cli.Close(context.Background()) }()

	_, err = cli.Invoke(context.Background(), "hello.Say", unaryReq{Name: "zrpc"})
	if err == nil {
		t.Fatal("Invoke() error = nil, want non-nil")
	}
	if serveErr := <-errCh; serveErr != nil {
		t.Fatalf("fake server error = %v", serveErr)
	}
}

func TestServerCancelClosesAcceptedClientConnection(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	run := startServer(t, srv)
	t.Cleanup(func() {
		run.cancel()
		if err := run.wait(); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() error = %v", err)
		}
	})

	cli := waitForClient(t, Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	run.cancel()
	if err := run.wait(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	ctx, cancelInvoke := context.WithTimeout(context.Background(), time.Second)
	defer cancelInvoke()
	_, err := cli.Invoke(ctx, "missing.Method", unaryReq{Name: "zrpc"})
	if err == nil {
		t.Fatal("Invoke() after server cancel error = nil, want non-nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Invoke() after server cancel waited for deadline")
	}
}

func serveInBackground(t *testing.T, srv *server.Server) context.CancelFunc {
	t.Helper()
	run := startServer(t, srv)
	t.Cleanup(func() {
		run.cancel()
		if err := run.wait(); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() error = %v", err)
		}
	})
	return run.cancel
}

type serverRun struct {
	cancel context.CancelFunc
	errCh  chan error
	once   sync.Once
	err    error
}

func startServer(t *testing.T, srv *server.Server) *serverRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()
	return &serverRun{cancel: cancel, errCh: errCh}
}

func (r *serverRun) wait() error {
	r.once.Do(func() {
		select {
		case r.err = <-r.errCh:
		case <-time.After(time.Second):
			r.err = errors.New("Serve() did not stop after context cancellation")
		}
	})
	return r.err
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
