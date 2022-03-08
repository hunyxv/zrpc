package zrpc

import "time"

// 注册服务时使用 json 序列化，以便查看

// RegisterConfig 服务注册所需配置
type RegisterConfig struct {
	Registries          []string      // 注册中心 endpoint
	ServicePrefix       string        // 服务前缀
	HeartBeatPeriod     time.Duration // 心跳间隔
	ServerInfo          Node
	HealthCheckEndPoint string // 注册中心进行健康检测回调的地址（Consul可能会用到）
	Logger              Logger
}

// ServiceRegister 服务注册
type ServiceRegister interface {
	// Register 注册节点
	Register()
	// Deregister 注销节点
	Deregister()
}

// DiscoverConfig 服务发现所需配置
type DiscoverConfig struct {
	Registries          []string // 注册中心 endpoint
	ServicePrefix       string   // 服务前缀
	ServiceName         string
	HeartBeatPeriod     time.Duration
	HealthCheckEndPoint string // 注册中心进行健康检测回调的地址（Consul可能会用到）
	Logger              Logger
}

// ServiceDiscover 服务发现
type ServiceDiscover interface {
	// Watch 监控节点变化
	Watch(callback WatchCallback)
	// Stop 停止监控
	Stop()
}

// WatchCallback 服务发现，节点变更事件回调接口
type WatchCallback interface {
	AddOrUpdate(endpoint string, metadata []byte) error
	Delete(endpoint string)
}

// RegisterDiscover 服务注册与发现
type RegisterDiscover interface {
	ServiceRegister
	ServiceDiscover
}
