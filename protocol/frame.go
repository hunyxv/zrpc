package protocol

import (
	"errors"
	"fmt"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/status"
)

type FrameType uint8

const (
	FrameRequest FrameType = iota + 1
	FrameResponse
	FrameData
	FrameWindowUpdate
	FrameEnd
	FrameReset
	FramePing
	FrameGoAway
)

type Direction uint8

const (
	DirectionNone Direction = iota
	DirectionClientToServer
	DirectionServerToClient
)

type Frame struct {
	Type      FrameType
	StreamID  string
	Seq       uint64
	Direction Direction
	Metadata  metadata.MD
	Payload   []byte
	Window    int
	Status    *status.Status
}

func (f Frame) Validate() error {
	if f.Type == 0 {
		return errors.New("protocol: frame type is required")
	}
	if f.Type > FrameGoAway {
		return fmt.Errorf("protocol: unknown frame type %d", f.Type)
	}
	if f.Direction > DirectionServerToClient {
		return fmt.Errorf("protocol: invalid direction %d", f.Direction)
	}
	if f.Type != FramePing && f.Type != FrameGoAway && f.StreamID == "" {
		return errors.New("protocol: stream id is required")
	}
	if f.Type == FrameWindowUpdate && f.Window <= 0 {
		return errors.New("protocol: window update must be positive")
	}
	return nil
}
