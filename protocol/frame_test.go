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

func TestFrameValidateConnectionControl(t *testing.T) {
	controlTypes := []struct {
		name      string
		frameType FrameType
	}{
		{name: "ping", frameType: FramePing},
		{name: "pong", frameType: FramePong},
		{name: "go away", frameType: FrameGoAway},
		{name: "close", frameType: FrameClose},
		{name: "close ack", frameType: FrameCloseAck},
	}

	for _, tt := range controlTypes {
		t.Run(tt.name, func(t *testing.T) {
			frame := Frame{Type: tt.frameType, Seq: 1}
			if err := frame.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	for _, tt := range controlTypes {
		t.Run(tt.name+" requires seq", func(t *testing.T) {
			frame := Frame{Type: tt.frameType}
			if err := frame.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want non-nil")
			}
		})
	}

	invalidFields := []struct {
		name   string
		mutate func(*Frame)
	}{
		{name: "stream id", mutate: func(frame *Frame) { frame.StreamID = "stream-1" }},
		{name: "metadata", mutate: func(frame *Frame) { frame.Metadata = metadata.MD{"key": {"value"}} }},
		{name: "payload", mutate: func(frame *Frame) { frame.Payload = []byte("payload") }},
		{name: "window", mutate: func(frame *Frame) { frame.Window = 1 }},
		{name: "status", mutate: func(frame *Frame) { frame.Status = &status.Status{} }},
		{name: "direction", mutate: func(frame *Frame) { frame.Direction = DirectionClientToServer }},
	}

	for _, controlType := range controlTypes {
		for _, invalidField := range invalidFields {
			t.Run(controlType.name+" with "+invalidField.name, func(t *testing.T) {
				frame := Frame{Type: controlType.frameType, Seq: 1}
				invalidField.mutate(&frame)
				if err := frame.Validate(); err == nil {
					t.Fatal("Validate() error = nil, want non-nil")
				}
			})
		}
	}
}
