package zrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

type streamChunk struct {
	Value string `msgpack:"value"`
}

type streamResult struct {
	Count int `msgpack:"count"`
}

func TestClientStreaming(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-client"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	srv.HandleStream("upload.Count", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		count := 0
		for {
			var chunk streamChunk
			err := stream.Recv(ctx, &chunk)
			if err == io.EOF {
				return stream.Send(ctx, streamResult{Count: count})
			}
			if err != nil {
				return err
			}
			count++
		}
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "upload.Count")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "a"}); err != nil {
		t.Fatalf("Send(a) error = %v", err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "b"}); err != nil {
		t.Fatalf("Send(b) error = %v", err)
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	var result streamResult
	if err := stream.Recv(context.Background(), &result); err != nil {
		t.Fatalf("Recv(result) error = %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("count = %d, want 2", result.Count)
	}
}

func TestServerStreaming(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-server"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	srv.HandleStream("download.List", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		var req streamChunk
		if err := stream.Recv(ctx, &req); err != nil {
			return err
		}
		if err := stream.Send(ctx, streamChunk{Value: req.Value + "-1"}); err != nil {
			return err
		}
		if err := stream.Send(ctx, streamChunk{Value: req.Value + "-2"}); err != nil {
			return err
		}
		return stream.CloseSend(ctx)
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "download.List")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "item"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}

	var first streamChunk
	if err := stream.Recv(context.Background(), &first); err != nil {
		t.Fatalf("Recv(first) error = %v", err)
	}
	var second streamChunk
	if err := stream.Recv(context.Background(), &second); err != nil {
		t.Fatalf("Recv(second) error = %v", err)
	}
	if first.Value != "item-1" || second.Value != "item-2" {
		t.Fatalf("chunks = %q, %q", first.Value, second.Value)
	}
	var end streamChunk
	if err := stream.Recv(context.Background(), &end); err != io.EOF {
		t.Fatalf("Recv(end) error = %v, want io.EOF", err)
	}
}

func TestBidiStreaming(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-bidi"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	srv.HandleStream("chat.Echo", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		for {
			var in streamChunk
			err := stream.Recv(ctx, &in)
			if err == io.EOF {
				return stream.CloseSend(ctx)
			}
			if err != nil {
				return err
			}
			if err := stream.Send(ctx, streamChunk{Value: in.Value + "!"}); err != nil {
				return err
			}
		}
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "chat.Echo")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "a"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	var out streamChunk
	if err := stream.Recv(context.Background(), &out); err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if out.Value != "a!" {
		t.Fatalf("value = %q, want a!", out.Value)
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	var end streamChunk
	if err := stream.Recv(context.Background(), &end); err != io.EOF {
		t.Fatalf("Recv(end) error = %v, want io.EOF", err)
	}
}

func serveStreamingInBackground(t *testing.T, srv *server.Server) context.CancelFunc {
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

func waitForStreamingClient(t *testing.T, opts client.Options) *client.Client {
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
