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
}
