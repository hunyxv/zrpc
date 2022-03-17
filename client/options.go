package client

import (
	"github.com/hunyxv/zrpc"
)

type ServerInfo struct {
	ServerName    string
	NodeID        string
	LocalEndpoint zrpc.Endpoint // 本地 endpoint
	StateEndpoint zrpc.Endpoint // 状态 endpoint
}

type Option func(opt *options)

type options struct {
	Discover   zrpc.ServiceDiscover // 服务发现组件
	ServerAddr *ServerInfo          //
	Identity   string               // client id
}

// WithDiscover 配置服务发现组件
func WithDiscover(d zrpc.ServiceDiscover) Option {
	return func(opt *options) {
		opt.Discover = d
	}
}

// WithServerAddr 配置 rpc server 地址
// 	如果配置了 服务发现组件，那么该设置不生效
func WithServerAddr(s *ServerInfo) Option {
	return func(opt *options) {
		opt.ServerAddr = s
	}
}

// WithIdentity 设置客户端id（不同客户端id不能相同）
func WithIdentity(id string) Option {
	return func(opt *options) {
		opt.Identity = id
	}
}
