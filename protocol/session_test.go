package protocol

import (
	"testing"

	"github.com/hunyxv/zrpc/status"
)

func TestSessionHalfClose(t *testing.T) {
	session := NewSession("stream-1")
	if session.IsClosed() {
		t.Fatal("new session is closed")
	}

	session.CloseSend()
	if session.CanSend() {
		t.Fatal("session can send after CloseSend")
	}
	if !session.CanRecv() {
		t.Fatal("session cannot recv after CloseSend")
	}
	if !session.SendClosed() {
		t.Fatal("send side is not closed")
	}
	if session.RecvClosed() {
		t.Fatal("recv side closed before CloseRecv")
	}
	if session.IsClosed() {
		t.Fatal("session closed before recv side closed")
	}

	session.CloseRecv()
	if !session.IsClosed() {
		t.Fatal("session is not closed after both sides closed")
	}
}

func TestSessionTryCloseSendOnlyTransitionsOnce(t *testing.T) {
	session := NewSession("stream-1")

	if !session.TryCloseSend() {
		t.Fatal("first TryCloseSend() = false, want true")
	}
	if session.TryCloseSend() {
		t.Fatal("second TryCloseSend() = true, want false")
	}
	if session.CanSend() {
		t.Fatal("session can send after TryCloseSend")
	}
}

func TestSessionTryCloseRecvOnlyTransitionsOnce(t *testing.T) {
	session := NewSession("stream-1")

	if !session.TryCloseRecv() {
		t.Fatal("first TryCloseRecv() = false, want true")
	}
	if session.TryCloseRecv() {
		t.Fatal("second TryCloseRecv() = true, want false")
	}
	if session.CanRecv() {
		t.Fatal("session can recv after TryCloseRecv")
	}
}

func TestSessionReset(t *testing.T) {
	session := NewSession("stream-1")
	st := &status.Status{Code: status.Unavailable, Message: "closed"}

	session.Reset(st)

	if !session.IsReset() {
		t.Fatal("session is not reset")
	}
	if !session.IsClosed() {
		t.Fatal("reset session is not closed")
	}
	got := session.ResetStatus()
	if got == nil {
		t.Fatal("ResetStatus() = nil")
	}
	if got.Code != status.Unavailable || got.Message != "closed" {
		t.Fatalf("ResetStatus() = %#v", got)
	}
	got.Message = "mutated"
	if session.ResetStatus().Message != "closed" {
		t.Fatal("ResetStatus() returned shared status")
	}
	if session.CanSend() {
		t.Fatal("session can send after Reset")
	}
	if session.CanRecv() {
		t.Fatal("session can recv after Reset")
	}
}

func TestSessionTryResetOnlyTransitionsOnce(t *testing.T) {
	session := NewSession("stream-1")
	first := &status.Status{Code: status.Unavailable, Message: "first"}
	second := &status.Status{Code: status.Internal, Message: "second"}

	if !session.TryReset(first) {
		t.Fatal("first TryReset() = false, want true")
	}
	if session.TryReset(second) {
		t.Fatal("second TryReset() = true, want false")
	}
	if got := session.ResetStatus(); got == nil || got.Message != "first" {
		t.Fatalf("ResetStatus() = %#v, want first status", got)
	}
}
