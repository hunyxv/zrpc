package zmq

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
)

func TestZMQTransportFrameRoundTrip(t *testing.T) {
	endpoint := testEndpoint(t)
	tr := New(Options{SndHWM: 100, RcvHWM: 100, Linger: 100 * time.Millisecond, RouterMandatory: true, Immediate: true})
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close(context.Background()) }()

	serverConnCh := make(chan transport.Conn, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err == nil {
			serverConnCh <- conn
		}
	}()

	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = clientConn.Close(context.Background()) }()
	serverConn := waitForServerConn(t, serverConnCh)
	defer func() { _ = serverConn.Close(context.Background()) }()

	clientStream, err := clientConn.OpenStream(context.Background(), "hello.Say", nil)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	serverStream, err := serverConn.AcceptStream(context.Background())
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}
	if _, err := serverStream.RecvFrame(context.Background()); err != nil {
		t.Fatalf("RecvFrame() request error = %v", err)
	}

	frame := &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID(), Payload: []byte("hello")}
	if err := clientStream.SendFrame(context.Background(), frame); err != nil {
		t.Fatalf("SendFrame() error = %v", err)
	}
	got, err := serverStream.RecvFrame(context.Background())
	if err != nil {
		t.Fatalf("RecvFrame() error = %v", err)
	}
	if string(got.Payload) != "hello" {
		t.Fatalf("payload = %q", got.Payload)
	}
}

func TestZMQOpenStreamSendsRequestFrame(t *testing.T) {
	endpoint := testEndpoint(t)
	tr := New(Options{Linger: 100 * time.Millisecond, RouterMandatory: true, Immediate: true})
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close(context.Background()) }()
	serverConnCh := make(chan transport.Conn, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err == nil {
			serverConnCh <- conn
		}
	}()
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = clientConn.Close(context.Background()) }()
	serverConn := waitForServerConn(t, serverConnCh)

	md := metadata.New()
	md.Set("Trace-ID", "trace-1")
	clientStream, err := clientConn.OpenStream(context.Background(), "hello.Say", md)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	md.Set("Trace-ID", "mutated")
	serverStream, err := serverConn.AcceptStream(context.Background())
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}
	requestFrame, err := serverStream.RecvFrame(context.Background())
	if err != nil {
		t.Fatalf("RecvFrame() error = %v", err)
	}
	if requestFrame.Type != protocol.FrameRequest {
		t.Fatalf("frame.Type = %v, want %v", requestFrame.Type, protocol.FrameRequest)
	}
	if requestFrame.StreamID != clientStream.ID() {
		t.Fatalf("stream id = %q, want %q", requestFrame.StreamID, clientStream.ID())
	}
	if got := requestFrame.Metadata.Get(transport.MethodMetadataKey); got != "hello.Say" {
		t.Fatalf("method metadata = %q", got)
	}
	if got := requestFrame.Metadata.Get("trace-id"); got != "trace-1" {
		t.Fatalf("trace metadata = %q", got)
	}
}

func TestZMQCloseStopsOpenAndAccept(t *testing.T) {
	endpoint := testEndpoint(t)
	tr := New(Options{Linger: 100 * time.Millisecond, RouterMandatory: true, Immediate: true})
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	serverConnCh := make(chan transport.Conn, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err == nil {
			serverConnCh <- conn
		}
	}()
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn := waitForServerConn(t, serverConnCh)

	if err := clientConn.Close(context.Background()); err != nil {
		t.Fatalf("client Close() error = %v", err)
	}
	if _, err := clientConn.OpenStream(context.Background(), "hello.Say", nil); err == nil {
		t.Fatal("OpenStream() after Close error = nil, want non-nil")
	}
	if err := listener.Close(context.Background()); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}
	if _, err := serverConn.AcceptStream(context.Background()); err == nil {
		t.Fatal("AcceptStream() after listener Close error = nil, want non-nil")
	}
}

func TestZMQTransportSupportsUnaryClientServer(t *testing.T) {
	endpoint := testEndpoint(t)
	tr := New(Options{Linger: 100 * time.Millisecond, RouterMandatory: true, Immediate: true})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	srv.HandleUnary("hello.Say", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		var in struct {
			Name string `msgpack:"name"`
		}
		if err := req.Decode(&in); err != nil {
			return nil, err
		}
		return zrpc.NewResponse(struct {
			Message string `msgpack:"message"`
		}{Message: "hello " + in.Name}, codec.Msgpack())
	}))
	run := startServer(t, srv)
	defer run.cancel()

	cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := cli.Invoke(context.Background(), "hello.Say", struct {
		Name string `msgpack:"name"`
	}{Name: "zmq"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	var out struct {
		Message string `msgpack:"message"`
	}
	if err := resp.Decode(&out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if out.Message != "hello zmq" {
		t.Fatalf("message = %q", out.Message)
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

func waitForServerConn(t *testing.T, ch <-chan transport.Conn) transport.Conn {
	t.Helper()
	select {
	case conn := <-ch:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("listener.Accept() did not return a server connection")
		return nil
	}
}
