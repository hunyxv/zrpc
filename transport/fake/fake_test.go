package fake

import (
	"context"
	"errors"
	"testing"
	"time"

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
	return clientStream, serverStream
}
