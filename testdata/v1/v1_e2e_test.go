package v1

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
	"github.com/hunyxv/zrpc/typed"
)

type e2eReq struct {
	Name string `msgpack:"name"`
}

type e2eResp struct {
	Message string `msgpack:"message"`
}

func TestV1UnaryE2E(t *testing.T) {
	endpoint := testEndpoint(t)
	tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: 100 * time.Millisecond, RouterMandatory: true, Immediate: true, HandshakeTimeout: 100 * time.Millisecond})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	typed.HandleUnary[e2eReq, e2eResp](srv, "hello.Say", func(ctx context.Context, req *e2eReq) (*e2eResp, error) {
		return &e2eResp{Message: "hello " + req.Name}, nil
	})
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := typed.Invoke[e2eReq, e2eResp](context.Background(), cli, "hello.Say", &e2eReq{Name: "zrpc"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if resp.Message != "hello zrpc" {
		t.Fatalf("message = %q", resp.Message)
	}
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
	run := &serverRun{cancel: cancel, errCh: errCh}
	t.Cleanup(func() {
		run.cancel()
		if err := run.wait(); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve() error = %v", err)
		}
	})
	return run
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

func waitForClient(t *testing.T, opts client.Options) *client.Client {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cli, err := client.New(opts)
		if err == nil {
			return cli
		}
		lastErr = err
	}
	t.Fatalf("client.New() did not connect: %v", lastErr)
	return nil
}

func testEndpoint(t *testing.T) transport.Endpoint {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return transport.Endpoint{Transport: "zmq", Address: "tcp://" + addr}
}
