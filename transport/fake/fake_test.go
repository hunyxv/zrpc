package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

func TestFakeTransportOpenAcceptStream(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", nil)
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

func TestFakeOpenStreamSendsRequestFrame(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	md := metadata.New()
	md.Set("Trace-ID", "trace-1")
	clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", md)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	md.Set("Trace-ID", "mutated")
	serverStream, err := serverConn.AcceptStream(context.Background())
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}
	frame, err := serverStream.RecvFrame(context.Background())
	if err != nil {
		t.Fatalf("RecvFrame() error = %v", err)
	}
	if frame.Type != protocol.FrameRequest {
		t.Fatalf("frame.Type = %v, want %v", frame.Type, protocol.FrameRequest)
	}
	if frame.StreamID != clientStream.ID() {
		t.Fatalf("frame.StreamID = %q, want %q", frame.StreamID, clientStream.ID())
	}
	if got := frame.Metadata.Get(transport.MethodMetadataKey); got != "user.Get" {
		t.Fatalf("method metadata = %q", got)
	}
	if got := frame.Metadata.Get("trace-id"); got != "trace-1" {
		t.Fatalf("trace metadata = %q", got)
	}
}

func TestFakeTransportDialMissingListener(t *testing.T) {
	tr := New()

	_, err := tr.Dial(context.Background(), transport.Endpoint{Transport: "fake", Address: "missing"}, transport.DialOptions{})
	if err == nil {
		t.Fatal("Dial() error = nil, want non-nil")
	}
}

func TestFakeTransportRejectsDuplicateListener(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	if _, err := tr.Listen(endpoint, transport.ListenOptions{}); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if _, err := tr.Listen(endpoint, transport.ListenOptions{}); err == nil {
		t.Fatal("second Listen() error = nil, want non-nil")
	}
}

func TestFakeListenerCloseStopsDialAndAccept(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := listener.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{}); err == nil {
		t.Fatal("Dial() error = nil, want non-nil")
	}
	if _, err := listener.Accept(context.Background()); err == nil {
		t.Fatal("Accept() error = nil, want non-nil")
	}
}

func TestFakeListenerCloseRejectsQueuedAccepts(t *testing.T) {
	for range 100 {
		tr := New()
		endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
		listener, err := tr.Listen(endpoint, transport.ListenOptions{})
		if err != nil {
			t.Fatalf("Listen() error = %v", err)
		}
		clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
		if err != nil {
			t.Fatalf("Dial() error = %v", err)
		}
		if err := clientConn.Close(context.Background()); err != nil {
			t.Fatalf("client Close() error = %v", err)
		}
		if err := listener.Close(context.Background()); err != nil {
			t.Fatalf("listener Close() error = %v", err)
		}
		if _, err := listener.Accept(context.Background()); err == nil {
			t.Fatal("Accept() error = nil, want non-nil")
		}
	}
}

func TestFakeListenerAcceptContextCanceled(t *testing.T) {
	tr := New()
	listener, err := tr.Listen(transport.Endpoint{Transport: "fake", Address: "svc"}, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = listener.Accept(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Accept() error = %v, want %v", err, context.Canceled)
	}
}

func TestFakeCanceledContextPreventsReadyOperations(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tr.Dial(ctx, endpoint, transport.DialOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial() error = %v, want %v", err, context.Canceled)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if _, err := clientConn.OpenStream(ctx, "user.Get", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenStream() error = %v, want %v", err, context.Canceled)
	}
	clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", nil)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	serverStream, err := serverConn.AcceptStream(context.Background())
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}
	if _, err := serverStream.RecvFrame(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecvFrame() error = %v, want %v", err, context.Canceled)
	}
	if err := clientStream.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SendFrame() error = %v, want %v", err, context.Canceled)
	}
	if err := serverConn.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain() error = %v, want %v", err, context.Canceled)
	}
}

func TestFakeStreamResetSendsResetFrame(t *testing.T) {
	clientStream, serverStream := newStreamPair(t)
	resetStatus := &status.Status{Code: status.Unavailable, Message: "closed"}

	if err := clientStream.Reset(context.Background(), resetStatus); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	frame, err := serverStream.RecvFrame(context.Background())
	if err != nil {
		t.Fatalf("RecvFrame() error = %v", err)
	}
	if frame.Type != protocol.FrameReset {
		t.Fatalf("frame.Type = %v, want %v", frame.Type, protocol.FrameReset)
	}
	if frame.Status == nil || frame.Status.Code != status.Unavailable || frame.Status.Message != "closed" {
		t.Fatalf("frame.Status = %#v", frame.Status)
	}
	if err := clientStream.SendFrame(context.Background(), &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID()}); err == nil {
		t.Fatal("SendFrame() after Reset error = nil, want non-nil")
	}
	if _, err := serverStream.RecvFrame(context.Background()); err == nil {
		t.Fatal("RecvFrame() after Reset frame error = nil, want non-nil")
	}
}

func TestFakeStreamCloseCausesSendAndRecvErrors(t *testing.T) {
	clientStream, serverStream := newStreamPair(t)

	if err := serverStream.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	err := clientStream.SendFrame(context.Background(), &protocol.Frame{
		Type:     protocol.FrameData,
		StreamID: clientStream.ID(),
		Payload:  []byte("after-close"),
	})
	if err == nil {
		t.Fatal("SendFrame() error = nil, want non-nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = serverStream.RecvFrame(ctx)
	if err == nil {
		t.Fatal("RecvFrame() error = nil, want non-nil")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("RecvFrame() waited for context deadline after Close")
	}
}

func TestFakeConnCloseStopsOpenAndAcceptStream(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	if err := clientConn.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := clientConn.OpenStream(context.Background(), "user.Get", nil); err == nil {
		t.Fatal("OpenStream() error = nil, want non-nil")
	}
	if err := serverConn.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := serverConn.AcceptStream(context.Background()); err == nil {
		t.Fatal("AcceptStream() error = nil, want non-nil")
	}
}

func TestFakeConnCloseClosesPeerAndExistingStreams(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", nil)
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

	if err := clientConn.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := serverConn.AcceptStream(context.Background()); err == nil {
		t.Fatal("peer AcceptStream() error = nil, want non-nil")
	}
	if err := clientStream.SendFrame(context.Background(), &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID()}); err == nil {
		t.Fatal("SendFrame() error = nil, want non-nil")
	}
	if _, err := serverStream.RecvFrame(context.Background()); err == nil {
		t.Fatal("RecvFrame() error = nil, want non-nil")
	}
}

func TestFakeDrainRejectsNewStreams(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if err := serverConn.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if _, err := clientConn.OpenStream(context.Background(), "user.Get", nil); err == nil {
		t.Fatal("OpenStream() after peer Drain error = nil, want non-nil")
	}
}

func TestFakeEndpointsDistinguishLocalAndRemote(t *testing.T) {
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if clientConn.RemoteEndpoint() != endpoint {
		t.Fatalf("client remote endpoint = %#v, want %#v", clientConn.RemoteEndpoint(), endpoint)
	}
	if serverConn.LocalEndpoint() != endpoint {
		t.Fatalf("server local endpoint = %#v, want %#v", serverConn.LocalEndpoint(), endpoint)
	}
	if serverConn.RemoteEndpoint() != clientConn.LocalEndpoint() {
		t.Fatalf("server remote endpoint = %#v, want client local %#v", serverConn.RemoteEndpoint(), clientConn.LocalEndpoint())
	}
	if clientConn.LocalEndpoint() == clientConn.RemoteEndpoint() {
		t.Fatalf("client local endpoint should differ from remote: %#v", clientConn.LocalEndpoint())
	}
}

func TestFakeSendFrameValidatesAndClones(t *testing.T) {
	clientStream, serverStream := newStreamPair(t)

	if err := clientStream.SendFrame(context.Background(), nil); err == nil {
		t.Fatal("SendFrame(nil) error = nil, want non-nil")
	}
	if err := clientStream.SendFrame(context.Background(), &protocol.Frame{Type: protocol.FrameData}); err == nil {
		t.Fatal("SendFrame(invalid) error = nil, want non-nil")
	}
	if err := clientStream.SendFrame(context.Background(), &protocol.Frame{Type: protocol.FrameData, StreamID: "other"}); err == nil {
		t.Fatal("SendFrame(wrong stream id) error = nil, want non-nil")
	}

	md := metadata.New()
	md.Set("Key", "original")
	payload := []byte("original")
	frame := &protocol.Frame{
		Type:     protocol.FrameData,
		StreamID: clientStream.ID(),
		Metadata: md,
		Payload:  payload,
		Status:   &status.Status{Code: status.Unavailable, Message: "before", Details: []string{"d1"}},
	}
	if err := clientStream.SendFrame(context.Background(), frame); err != nil {
		t.Fatalf("SendFrame() error = %v", err)
	}
	md.Set("Key", "mutated")
	payload[0] = 'X'
	frame.Status.Message = "after"
	frame.Status.Details[0] = "d2"

	got, err := serverStream.RecvFrame(context.Background())
	if err != nil {
		t.Fatalf("RecvFrame() error = %v", err)
	}
	if got.Metadata.Get("key") != "original" {
		t.Fatalf("metadata = %#v", got.Metadata)
	}
	if string(got.Payload) != "original" {
		t.Fatalf("payload = %q", got.Payload)
	}
	if got.Status == nil || got.Status.Message != "before" || got.Status.Details[0] != "d1" {
		t.Fatalf("status = %#v", got.Status)
	}
}

func newStreamPair(t *testing.T) (transport.TransportStream, transport.TransportStream) {
	t.Helper()
	tr := New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
	listener, err := tr.Listen(endpoint, transport.ListenOptions{})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	serverConn, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", nil)
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
	return clientStream, serverStream
}
