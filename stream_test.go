package zrpc_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/status"
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
	var end streamResult
	if err := stream.Recv(context.Background(), &end); err != io.EOF {
		t.Fatalf("Recv(end) error = %v, want io.EOF", err)
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

func TestStreamingPayloadExceedingWindowDoesNotDeadlock(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-window"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 64})
	srv.HandleStream("upload.Bytes", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
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
			count += len(chunk.Value)
		}
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 64})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "upload.Bytes")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := stream.Send(ctx, streamChunk{Value: strings.Repeat("x", 40)})
		cancel()
		if err != nil {
			t.Fatalf("Send(%d) error = %v", i, err)
		}
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	var result streamResult
	if err := stream.Recv(context.Background(), &result); err != nil {
		t.Fatalf("Recv(result) error = %v", err)
	}
	if result.Count != 160 {
		t.Fatalf("count = %d, want 160", result.Count)
	}
	var end streamResult
	if err := stream.Recv(context.Background(), &end); err != io.EOF {
		t.Fatalf("Recv(end) error = %v, want io.EOF", err)
	}
}

func TestStreamSendAfterCloseSendFails(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-close"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleStream("upload.Ignore", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		for {
			var chunk streamChunk
			err := stream.Recv(ctx, &chunk)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "upload.Ignore")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "late"}); err == nil {
		t.Fatal("Send() after CloseSend error = nil, want non-nil")
	}
}

func TestStreamResetIsSticky(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-reset"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleStream("stream.Reset", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		return status.Error(status.PermissionDenied, "denied")
	}))
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "stream.Reset")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	var out streamChunk
	err = stream.Recv(context.Background(), &out)
	if st := status.FromError(err); st.Code != status.PermissionDenied {
		t.Fatalf("first Recv status = %#v, err=%v", st, err)
	}
	err = stream.Recv(context.Background(), &out)
	if st := status.FromError(err); st.Code != status.PermissionDenied {
		t.Fatalf("second Recv status = %#v, err=%v", st, err)
	}
	if err := stream.Send(context.Background(), streamChunk{Value: "late"}); err == nil {
		t.Fatal("Send() after reset error = nil, want non-nil")
	}
}

func TestUnknownStreamingMethodReturnsUnimplemented(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "stream-missing"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	cancel := serveStreamingInBackground(t, srv)
	defer cancel()

	cli := waitForStreamingClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := cli.NewStream(context.Background(), "missing.Stream")
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	var out streamChunk
	err = stream.Recv(context.Background(), &out)
	if st := status.FromError(err); st.Code != status.Unimplemented {
		t.Fatalf("Recv status = %#v, err=%v", st, err)
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
