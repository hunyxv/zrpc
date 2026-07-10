package protocol

import (
	"testing"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/status"
)

func TestFrameValidateRequest(t *testing.T) {
	frame := Frame{
		Type:     FrameRequest,
		StreamID: "stream-1",
		Metadata: metadata.MD{
			"method": {"/test.Service/Method"},
		},
		Payload: []byte("request"),
	}

	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFrameValidateRequiresStreamID(t *testing.T) {
	frame := Frame{
		Type: FrameData,
	}

	if err := frame.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestStatusFrame(t *testing.T) {
	frame := Frame{
		Type:     FrameReset,
		StreamID: "stream-1",
		Status: &status.Status{
			Code:    status.Unavailable,
			Message: "closed",
		},
	}

	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFrameValidateRejectsUnknownType(t *testing.T) {
	frame := Frame{Type: FrameType(255), StreamID: "stream-1"}
	if err := frame.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestFrameValidateRejectsInvalidDirection(t *testing.T) {
	frame := Frame{
		Type:      FrameData,
		StreamID:  "stream-1",
		Direction: Direction(255),
	}
	if err := frame.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestFrameValidateWindowUpdateRequiresPositiveWindow(t *testing.T) {
	frame := Frame{
		Type:     FrameWindowUpdate,
		StreamID: "stream-1",
		Window:   0,
	}
	if err := frame.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}

	frame.Window = 1
	if err := frame.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
