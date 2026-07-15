package server

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

func TestMaxConcurrentStreamsRejectsNewStreamWhenLimitReached(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "server-max-concurrent-streams"}
	srv := New(Options{
		Transport:            tr,
		Endpoint:             endpoint,
		Codec:                codec.Msgpack(),
		MaxConcurrentStreams: 1,
	})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv.HandleStream("slow.Block", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		entered <- struct{}{}
		<-release
		return nil
	}))
	run := startServerForConcurrencyTest(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()

	first, err := cli.NewStream(context.Background(), "slow.Block")
	if err != nil {
		t.Fatalf("first NewStream() error = %v", err)
	}
	waitForStreamHandler(t, entered)

	second, err := cli.NewStream(context.Background(), "slow.Block")
	if err != nil {
		t.Fatalf("second NewStream() error = %v", err)
	}
	var out struct{}
	err = recvWithTimeout(t, second, &out)
	if st := status.FromError(err); st.Code != status.ResourceExhausted {
		t.Fatalf("second Recv() status = %#v, err=%v, want ResourceExhausted", st, err)
	}

	close(release)
	if err := recvWithTimeout(t, first, &out); !errors.Is(err, io.EOF) {
		t.Fatalf("first Recv() after release error = %v, want EOF", err)
	}

	third, err := cli.NewStream(context.Background(), "slow.Block")
	if err != nil {
		t.Fatalf("third NewStream() error = %v", err)
	}
	waitForStreamHandler(t, entered)
	if err := recvWithTimeout(t, third, &out); !errors.Is(err, io.EOF) {
		t.Fatalf("third Recv() error = %v, want EOF", err)
	}
}

func TestMaxConcurrentStreamsZeroDoesNotLimitStreams(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "server-max-concurrent-streams-zero"}
	srv := New(Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv.HandleStream("slow.Block", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		entered <- struct{}{}
		<-release
		return nil
	}))
	run := startServerForConcurrencyTest(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()

	first, err := cli.NewStream(context.Background(), "slow.Block")
	if err != nil {
		t.Fatalf("first NewStream() error = %v", err)
	}
	second, err := cli.NewStream(context.Background(), "slow.Block")
	if err != nil {
		t.Fatalf("second NewStream() error = %v", err)
	}
	waitForStreamHandler(t, entered)
	waitForStreamHandler(t, entered)

	close(release)
	var out struct{}
	if err := recvWithTimeout(t, first, &out); !errors.Is(err, io.EOF) {
		t.Fatalf("first Recv() error = %v, want EOF", err)
	}
	if err := recvWithTimeout(t, second, &out); !errors.Is(err, io.EOF) {
		t.Fatalf("second Recv() error = %v, want EOF", err)
	}
}

type concurrencyServerRun struct {
	cancel context.CancelFunc
	errCh  chan error
}

func startServerForConcurrencyTest(t *testing.T, srv *Server) *concurrencyServerRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx)
	}()
	run := &concurrencyServerRun{cancel: cancel, errCh: errCh}
	t.Cleanup(func() {
		run.cancel()
		select {
		case err := <-run.errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Serve() did not stop")
		}
	})
	return run
}

func waitForStreamHandler(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not start")
	}
}

func recvWithTimeout(t *testing.T, stream zrpc.Stream, out any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return stream.Recv(ctx, out)
}
