package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metrics"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestServeRequiresTransportAndCodec(t *testing.T) {
	if err := New(Options{Codec: codec.Msgpack()}).Serve(context.Background()); err == nil {
		t.Fatal("Serve() without transport error = nil, want non-nil")
	}
	if err := New(Options{Transport: fake.New(), Endpoint: transport.Endpoint{Transport: "fake", Address: "svc"}}).Serve(context.Background()); err == nil {
		t.Fatal("Serve() without codec error = nil, want non-nil")
	}
}

func TestHandleUnaryRecordsMetrics(t *testing.T) {
	collector := &serverRecordingCollector{}
	srv := New(Options{Transport: fake.New(), Endpoint: transport.Endpoint{Transport: "fake", Address: "svc"}, Codec: codec.Msgpack(), Metrics: collector})
	srv.HandleUnary("hello.Fail", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		return nil, status.Error(status.PermissionDenied, "denied")
	}))

	req, err := zrpc.NewRequest("hello.Fail", struct{}{}, codec.Msgpack())
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	_, err = srv.invokeUnaryForTest(context.Background(), req)
	if st := status.FromError(err); st.Code != status.PermissionDenied {
		t.Fatalf("status = %#v, err=%v", st, err)
	}
	if collector.starts != 1 || collector.finishes != 1 {
		t.Fatalf("metrics starts=%d finishes=%d", collector.starts, collector.finishes)
	}
	if collector.lastStatus == nil || collector.lastStatus.Code != status.PermissionDenied {
		t.Fatalf("status = %#v", collector.lastStatus)
	}
}

func TestUnknownUnaryRecordsMetrics(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	collector := &serverRecordingCollector{}
	srv := New(Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), Metrics: collector})

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
			t.Fatal("Serve() did not stop")
		}
	})

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	t.Cleanup(func() { _ = cli.Close(context.Background()) })
	_, err := cli.Invoke(context.Background(), "missing.Method", struct{}{})
	if st := status.FromError(err); st.Code != status.Unimplemented {
		t.Fatalf("status = %#v, err=%v", st, err)
	}
	if collector.starts != 1 || collector.finishes != 1 {
		t.Fatalf("metrics starts=%d finishes=%d", collector.starts, collector.finishes)
	}
	if collector.lastStatus == nil || collector.lastStatus.Code != status.Unimplemented {
		t.Fatalf("status = %#v", collector.lastStatus)
	}
}

func TestServerExtractsTraceContext(t *testing.T) {
	oldPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTextMapPropagator(oldPropagator)
	})

	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	wantTraceID := oteltrace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	var gotTraceID oteltrace.TraceID
	srv := New(Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleUnary("hello.Trace", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		gotTraceID = oteltrace.SpanContextFromContext(ctx).TraceID()
		return zrpc.NewResponse(struct{}{}, codec.Msgpack())
	}))

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
			t.Fatal("Serve() did not stop")
		}
	})

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	t.Cleanup(func() { _ = cli.Close(context.Background()) })
	callCtx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     oteltrace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
		TraceFlags: oteltrace.FlagsSampled,
	}))
	if _, err := cli.Invoke(callCtx, "hello.Trace", struct{}{}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if gotTraceID != wantTraceID {
		t.Fatalf("trace ID = %v, want %v", gotTraceID, wantTraceID)
	}
}

type serverRecordingCollector struct {
	starts     int
	finishes   int
	lastStatus *status.Status
}

func (c *serverRecordingCollector) OnRPCStart(ctx context.Context, info metrics.RPCInfo) {
	c.starts++
}

func (c *serverRecordingCollector) OnRPCFinish(ctx context.Context, info metrics.RPCInfo, st *status.Status, dur time.Duration) {
	c.finishes++
	c.lastStatus = st
}

func (c *serverRecordingCollector) OnStreamEvent(ctx context.Context, event metrics.StreamEvent) {}

func (c *serverRecordingCollector) OnTransportEvent(ctx context.Context, event metrics.TransportEvent) {
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
