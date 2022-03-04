package zrpc

import "time"

type Option func(opt *options)

type options struct {
	MaxTimeoutPeriod  time.Duration // 函数执行最大时间期限
	Logger            Logger        // logger
	Node              Node          // 节点信息：服务名称、监听地址
	HeartbeatInterval time.Duration // 节点间心跳间隔
}

// WithMaxTimeoutPeriod 函数执行最大时间期限
func WithMaxTimeoutPeriod(t time.Duration) Option {
	return func(opt *options) {
		opt.MaxTimeoutPeriod = t
	}
}

// WithLogger 设置 logger
func WithLogger(logger Logger) Option {
	return func(opt *options) {
		opt.Logger = logger
	}
}

// WithNodeInfo 设置启动节点
func WithNodeInfo(node Node) Option {
	return func(opt *options) {
		opt.Node = node
	}
}

// WithHeartbeatInterval 设置节点间心跳间隔
func WithHeartbeatInterval(t time.Duration) Option {
	return func(opt *options) {
		opt.HeartbeatInterval = t
	}
}
