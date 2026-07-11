package typed

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

type helloReq struct {
	Name string `msgpack:"name"`
}

type helloResp struct {
	Message string `msgpack:"message"`
}

func TestTypedUnary(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-unary"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	HandleUnary[helloReq, helloResp](srv, "hello.Say", func(ctx context.Context, req *helloReq) (*helloResp, error) {
		return &helloResp{Message: "hello " + req.Name}, nil
	})
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := Invoke[helloReq, helloResp](context.Background(), cli, "hello.Say", &helloReq{Name: "typed"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if resp.Message != "hello typed" {
		t.Fatalf("message = %q", resp.Message)
	}
}

func TestTypedClientStream(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-client-stream"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	HandleClientStream[helloReq, helloResp](srv, "upload.Count", func(ctx context.Context, stream *ServerStream[helloReq, helloResp]) error {
		count := 0
		for {
			_, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				return stream.SendAndClose(ctx, &helloResp{Message: strconv.Itoa(count)})
			}
			if err != nil {
				return err
			}
			count++
		}
	})
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := NewClientStream[helloReq, helloResp](context.Background(), cli, "upload.Count")
	if err != nil {
		t.Fatalf("NewClientStream() error = %v", err)
	}
	if err := stream.Send(context.Background(), &helloReq{Name: "a"}); err != nil {
		t.Fatalf("Send(a) error = %v", err)
	}
	if err := stream.Send(context.Background(), &helloReq{Name: "b"}); err != nil {
		t.Fatalf("Send(b) error = %v", err)
	}
	resp, err := stream.CloseAndRecv(context.Background())
	if err != nil {
		t.Fatalf("CloseAndRecv() error = %v", err)
	}
	if resp.Message != "2" {
		t.Fatalf("message = %q", resp.Message)
	}
}

func TestTypedServerStream(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-server-stream"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	HandleServerStream[helloReq, helloResp](srv, "hello.List", func(ctx context.Context, req *helloReq, stream *ServerSender[helloResp]) error {
		for i := 0; i < 3; i++ {
			if err := stream.Send(ctx, &helloResp{Message: req.Name + "-" + strconv.Itoa(i)}); err != nil {
				return err
			}
		}
		return nil
	})
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := NewServerStream[helloReq, helloResp](context.Background(), cli, "hello.List", &helloReq{Name: "item"})
	if err != nil {
		t.Fatalf("NewServerStream() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		resp, err := stream.Recv(context.Background())
		if err != nil {
			t.Fatalf("Recv(%d) error = %v", i, err)
		}
		want := "item-" + strconv.Itoa(i)
		if resp.Message != want {
			t.Fatalf("message = %q, want %q", resp.Message, want)
		}
	}
	if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() after stream end error = %v, want EOF", err)
	}
}

func TestTypedBidiStream(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-bidi-stream"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	HandleBidiStream[helloReq, helloResp](srv, "chat.Echo", func(ctx context.Context, stream *BidiServerStream[helloReq, helloResp]) error {
		for {
			req, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := stream.Send(ctx, &helloResp{Message: "echo " + req.Name}); err != nil {
				return err
			}
		}
	})
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := NewBidiStream[helloReq, helloResp](context.Background(), cli, "chat.Echo")
	if err != nil {
		t.Fatalf("NewBidiStream() error = %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if err := stream.Send(context.Background(), &helloReq{Name: name}); err != nil {
			t.Fatalf("Send(%s) error = %v", name, err)
		}
		resp, err := stream.Recv(context.Background())
		if err != nil {
			t.Fatalf("Recv(%s) error = %v", name, err)
		}
		if resp.Message != "echo "+name {
			t.Fatalf("message = %q", resp.Message)
		}
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() after close error = %v, want EOF", err)
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
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cli, err := client.New(opts)
		if err == nil {
			return cli
		}
		lastErr = err
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("client.New() did not connect: %v", lastErr)
	return nil
}
