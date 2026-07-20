package protocol

import (
	"errors"
	"fmt"

	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/status"
)

// FrameType 表示 transport frame 的类型。
type FrameType uint8

const (
	// FrameRequest 表示打开 stream 的请求帧。
	FrameRequest FrameType = iota + 1
	// FrameResponse 表示 unary 响应帧。
	FrameResponse
	// FrameData 表示 stream 数据帧。
	FrameData
	// FrameWindowUpdate 表示流控窗口更新帧。
	FrameWindowUpdate
	// FrameEnd 表示某个方向的 stream 正常结束。
	FrameEnd
	// FrameReset 表示 stream 被错误终止。
	FrameReset
	// FramePing 表示连接探测帧。
	FramePing
	// FrameGoAway 表示连接即将关闭或不再接受新 stream。
	FrameGoAway
	// FramePong 表示连接探测响应帧。
	FramePong
	// FrameClose 表示连接关闭请求帧。
	FrameClose
	// FrameCloseAck 表示连接关闭确认帧。
	FrameCloseAck
)

func (t FrameType) isConnectionControl() bool {
	switch t {
	case FramePing, FramePong, FrameGoAway, FrameClose, FrameCloseAck:
		return true
	default:
		return false
	}
}

// Direction 表示 frame 的逻辑传输方向。
type Direction uint8

const (
	// DirectionNone 表示 frame 不绑定业务方向。
	DirectionNone Direction = iota
	// DirectionClientToServer 表示客户端到服务端方向。
	DirectionClientToServer
	// DirectionServerToClient 表示服务端到客户端方向。
	DirectionServerToClient
)

// Frame 是 zrpc transport 层传输的最小协议单元。
type Frame struct {
	// Type 是 frame 类型。
	Type FrameType
	// StreamID 标识 frame 所属 stream。
	StreamID string
	// Seq 用作 stream 内顺序号，也用于关联连接控制请求和响应。
	Seq uint64
	// Direction 标识 frame 的逻辑传输方向。
	Direction Direction
	// Metadata 保存请求或响应元数据。
	Metadata metadata.MD
	// Payload 保存编码后的业务数据。
	Payload []byte
	// Window 保存窗口增量，仅用于 FrameWindowUpdate。
	Window int
	// Status 保存响应或 reset 状态。
	Status *status.Status
}

// Validate 校验 frame 的基本协议约束。
func (f Frame) Validate() error {
	if f.Type == 0 {
		return errors.New("protocol: frame type is required")
	}
	if f.Type > FrameCloseAck {
		return fmt.Errorf("protocol: unknown frame type %d", f.Type)
	}
	if f.Direction > DirectionServerToClient {
		return fmt.Errorf("protocol: invalid direction %d", f.Direction)
	}
	if f.Type.isConnectionControl() {
		if f.Seq == 0 {
			return errors.New("protocol: connection control frame seq is required")
		}
		if f.StreamID != "" || len(f.Metadata) != 0 || len(f.Payload) != 0 || f.Window != 0 || f.Status != nil || f.Direction != DirectionNone {
			return errors.New("protocol: connection control frame contains stream fields")
		}
		return nil
	}
	if f.StreamID == "" {
		return errors.New("protocol: stream id is required")
	}
	if f.Type == FrameWindowUpdate && f.Window <= 0 {
		return errors.New("protocol: window update must be positive")
	}
	return nil
}
