package zrpc

import (
	"github.com/panjf2000/ants/v2"
	"github.com/pborman/uuid"
)

var (
	DefaultNode Node

	defaultRPCInstance *RPCInstance

	goroutinePool *ants.Pool

	svcMultiplexer *SvcMultiplexer
)

func init() {
	var realIP string
	ips, _ := getLocalIps()
	if len(ips) > 0 {
		realIP = ips[0]
	} else {
		realIP = "0.0.0.0"
	}
	DefaultNode = Node{
		ServiceName:     getServerName(),
		NodeID:          uuid.NewUUID().String(),
		LocalEndpoint:   Endpoint{Scheme: "tcp", Host: realIP, Port: 10080},
		ClusterEndpoint: Endpoint{Scheme: "tcp", Host: realIP, Port: 10081},
		StateEndpoint:   Endpoint{Scheme: "tcp", Host: realIP, Port: 10082},
		IsIdle:          true,
	}
}

// RegisterServer 注册服务
func RegisterServer(name string, server any, conventions any) error {
	if defaultRPCInstance == nil {
		defaultRPCInstance = NewRPCInstance()
	}
	return defaultRPCInstance.RegisterServer(name, server, conventions)
}

// SetWorkPoolSize 设置工作池大小（默认无限大）
func SetWorkPoolSize(size int) (err error) {
	if goroutinePool == nil {
		goroutinePool, err = ants.NewPool(size, ants.WithNonblocking(true))
		return
	}
	goroutinePool.Tune(size)
	return
}

// Run 启动 zrpc 服务
func Run(opts ...Option) error {
	if goroutinePool == nil {
		if err := SetWorkPoolSize(0); err != nil {
			return err
		}
	}

	if defaultRPCInstance == nil {
		defaultRPCInstance = NewRPCInstance()
	}

	var err error
	svcMultiplexer, err = NewSvcMultiplexer(defaultRPCInstance, opts...)
	if err != nil {
		return err
	}
	svcMultiplexer.Run()
	return nil
}

// Close 关闭 zrpc 服务
func Close() {
	if svcMultiplexer == nil {
		return
	}

	svcMultiplexer.Close()
}
