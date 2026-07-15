package zmq

import "time"

// Options 描述 ZeroMQ socket 和 owner loop 的运行参数。
//
// 这些参数分为两层：
//   - ZeroMQ socket 层参数，如 SndHWM/RcvHWM/Linger/Immediate/RouterMandatory。
//   - Go owner 层参数，如 SendQueueSize/RecvQueueSize/HandshakeTimeout。
//
// 高并发场景下要同时关注两层队列。SndHWM/RcvHWM 只约束 libzmq 内部队列，
// SendQueueSize 和未来的 RecvQueueSize 用于约束 Go 进程内的排队内存。
type Options struct {
	// SndHWM 是 ZeroMQ 发送高水位。
	//
	// 当底层 peer 或网络无法及时消费时，libzmq 发送队列达到该上限后会返回 EAGAIN。
	// 当前 owner 收到 EAGAIN 会保留 pending，并在后续循环重试。
	SndHWM int
	// RcvHWM 是 ZeroMQ 接收高水位。
	//
	// 它限制 libzmq 接收队列的大小，但不限制 Go 层 stream.frames；
	// 因此高并发/慢消费者场景还需要 RecvQueueSize 真正落地。
	RcvHWM int
	// Linger 是 socket 关闭时等待未发送消息的时间。
	//
	// 较大的 Linger 有利于尽量送出已排队消息，但 Close 可能等待更久；
	// 较小或负值更偏向快速退出，可能丢弃未发送消息。
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
	// SendQueueSize 是 Go 层 owner send channel 大小。
	//
	// 这是业务 goroutine 进入 owner 前的第一道背压。队列满时 SendFrame/OpenStream
	// 会阻塞，直到 ctx 超时/取消或 owner 腾出容量。
	SendQueueSize int
	// RecvQueueSize 预留为 Go 层接收队列大小。
	//
	// 当前实现尚未强制使用该值，stream.frames 仍是无界队列。
	// 后续应将它应用到 conn incoming 队列和每个 stream 的 frame 队列。
	RecvQueueSize int
	// HandshakeTimeout 是客户端建连后发送 ping 的超时时间。
	//
	// Dial 成功连接 socket 后会发送 FramePing。该超时只覆盖 ping 进入 owner 并完成发送，
	// 不代表业务层认证、服务发现或端到端健康检查已经完成。
	HandshakeTimeout time.Duration
}

func defaultOptions(opts Options) Options {
	// 零值配置代表使用库默认值，便于调用方按需只覆盖关注的参数。
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
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = time.Second
	}
	return opts
}
