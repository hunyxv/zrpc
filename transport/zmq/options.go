package zmq

import (
	"fmt"
	"time"

	"github.com/hunyxv/zrpc/metrics"
)

// Options 描述 ZeroMQ socket 和 owner loop 的运行参数。
//
// 这些参数分为两层：
//   - ZeroMQ socket 层参数，如 SndHWM/RcvHWM/Linger/Immediate/RouterMandatory。
//   - Go owner 层参数，如发送预算、接收队列、心跳和关闭握手超时。
//
// 高并发场景下要同时关注两层队列。SndHWM/RcvHWM 只约束 libzmq 内部队列，
// SendQueueSize、SendQueueBytes 和 RecvQueueSize 用于约束 Go 进程内的排队内存。
type Options struct {
	// Metrics 接收 transport 生命周期和背压事件。
	Metrics metrics.Collector
	// SndHWM 是 ZeroMQ 发送高水位。
	//
	// 当底层 peer 或网络无法及时消费时，libzmq 发送队列达到该上限后会返回 EAGAIN。
	// 当前 owner 收到 EAGAIN 会保留一个受预算约束的 head，并在后续循环重试。
	SndHWM int
	// RcvHWM 是 ZeroMQ 接收高水位。
	//
	// 它限制 libzmq 接收队列的大小；已经进入 Go 进程的 frame 再由 RecvQueueSize 限制。
	RcvHWM int
	// Linger 是 socket 关闭时等待未发送消息的时间。
	//
	// 较大的 Linger 有利于尽量送出已排队消息，但 Close 可能等待更久；
	// 较小值更偏向快速退出；负值表示无限等待，不建议用于需要有界关闭的服务。
	Linger time.Duration
	// Immediate 控制是否只向已连接 peer 排队。
	//
	// true 时，ZeroMQ 只会向已经完成连接的 peer 排队，能更快暴露不可达目标；
	// false 时，消息可能先进入内部队列，直到连接建立或最终失败。
	Immediate bool
	// RouterMandatory 控制 ROUTER 对不可路由 identity 是否返回错误。
	//
	// true 时，服务端向不存在或已断开的 DEALER identity 发送会得到错误，
	// 便于上层把路由失败转成 stream reset/transport error，而不是静默丢消息。
	RouterMandatory bool
	// SendQueueSize 是 Go 层 owner 已准入数据 frame 的总数上限。
	//
	// 该上限同时覆盖 data channel 和 EAGAIN head。预算满时 SendFrame/OpenStream
	// 会等待，直到 ctx 超时/取消或 owner 释放容量。
	SendQueueSize int
	// SendQueueBytes 是 Go 层发送队列允许占用的最大编码字节数。
	SendQueueBytes int64
	// ControlQueueSize 是连接控制帧和流控制帧的独立准入上限。
	ControlQueueSize int
	// RecvQueueSize 是 Go 层接收队列大小，按 frame 个数计算。
	//
	// 它同时限制 conn incoming stream 队列和每个 stream 的 frame 队列。
	// 队列满时会向对端返回 ResourceExhausted reset；FrameEnd/FrameReset 可绕过该上限。
	RecvQueueSize int
	// HandshakeTimeout 是客户端建连后发送 ping 的超时时间。
	//
	// Dial 成功连接 socket 后会发送 FramePing。该超时只覆盖 ping 进入 owner 并完成发送，
	// 不代表业务层认证、服务发现或端到端健康检查已经完成。
	HandshakeTimeout time.Duration
	// HeartbeatInterval 是连接空闲多久后发送存活探测的间隔。
	HeartbeatInterval time.Duration
	// PeerTimeout 是多久未收到对端有效 frame 后判定连接失效。
	PeerTimeout time.Duration
	// CloseHandshakeTimeout 是 Close 等待对端确认的最长时间。
	CloseHandshakeTimeout time.Duration
}

func defaultOptions(opts Options) Options {
	// 零值配置代表使用库默认值，便于调用方按需只覆盖关注的参数。
	if opts.Metrics == nil {
		opts.Metrics = metrics.Noop()
	}
	if opts.SndHWM == 0 {
		opts.SndHWM = 1000
	}
	if opts.RcvHWM == 0 {
		opts.RcvHWM = 1000
	}
	if opts.Linger == 0 {
		opts.Linger = time.Second
	}
	if opts.SendQueueSize == 0 {
		opts.SendQueueSize = 1024
	}
	if opts.RecvQueueSize == 0 {
		opts.RecvQueueSize = 1024
	}
	if opts.SendQueueBytes == 0 {
		opts.SendQueueBytes = 64 << 20
	}
	if opts.ControlQueueSize == 0 {
		opts.ControlQueueSize = 128
	}
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = time.Second
	}
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = 10 * time.Second
	}
	if opts.PeerTimeout == 0 {
		opts.PeerTimeout = 30 * time.Second
	}
	if opts.CloseHandshakeTimeout == 0 {
		opts.CloseHandshakeTimeout = 5 * time.Second
	}
	return opts
}

func (opts Options) validate() error {
	if opts.SndHWM < 0 {
		return fmt.Errorf("zmq: SndHWM must not be negative: %d", opts.SndHWM)
	}
	if opts.RcvHWM < 0 {
		return fmt.Errorf("zmq: RcvHWM must not be negative: %d", opts.RcvHWM)
	}
	if opts.SendQueueSize < 0 {
		return fmt.Errorf("zmq: SendQueueSize must not be negative: %d", opts.SendQueueSize)
	}
	if opts.RecvQueueSize < 0 {
		return fmt.Errorf("zmq: RecvQueueSize must not be negative: %d", opts.RecvQueueSize)
	}
	if opts.SendQueueBytes < 0 {
		return fmt.Errorf("zmq: SendQueueBytes must not be negative: %d", opts.SendQueueBytes)
	}
	if opts.ControlQueueSize < 0 {
		return fmt.Errorf("zmq: ControlQueueSize must not be negative: %d", opts.ControlQueueSize)
	}
	if opts.HandshakeTimeout < 0 {
		return fmt.Errorf("zmq: HandshakeTimeout must not be negative: %s", opts.HandshakeTimeout)
	}
	if opts.HeartbeatInterval < 0 {
		return fmt.Errorf("zmq: HeartbeatInterval must not be negative: %s", opts.HeartbeatInterval)
	}
	if opts.PeerTimeout < 0 {
		return fmt.Errorf("zmq: PeerTimeout must not be negative: %s", opts.PeerTimeout)
	}
	if opts.CloseHandshakeTimeout < 0 {
		return fmt.Errorf("zmq: CloseHandshakeTimeout must not be negative: %s", opts.CloseHandshakeTimeout)
	}
	if opts.HeartbeatInterval > opts.PeerTimeout/2 {
		return fmt.Errorf("zmq: PeerTimeout (%s) must be at least twice HeartbeatInterval (%s)", opts.PeerTimeout, opts.HeartbeatInterval)
	}
	return nil
}
