package protocol

import "testing"

func TestSessionHalfClose(t *testing.T) {
	session := NewSession("stream-1")
	if session.IsClosed() {
		t.Fatal("new session is closed")
	}

	session.CloseSend()
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

	session.Reset()

	if !session.IsReset() {
		t.Fatal("session is not reset")
	}
	if !session.IsClosed() {
		t.Fatal("reset session is not closed")
	}
}
