package zrpc

import (
	"fmt"

	"github.com/panjf2000/ants/v2"
	"github.com/pborman/uuid"
)

var (
	ipaddr string

	DefaultNode Node

	defaultRPCInstance *RPCInstance

	goroutinePool *ants.Pool

	svcMultiplexer *SvcMultiplexer
)

func init() {
	ips, err := getLocalIps()
	if err != nil || len(ips) == 0 {
		ipaddr = "0.0.0.0"
	} else {
		ipaddr = ips[0]
	}

	DefaultNode = Node{
		ServiceName:     getServerName(),
		NodeID:          uuid.NewUUID().String(),
		LocalEndpoint:   fmt.Sprintf("tcp://%s:8080", ipaddr),
		ClusterEndpoint: fmt.Sprintf("tcp://%s:8081", ipaddr),
		StateEndpoint:   fmt.Sprintf("tcp://%s:8082", ipaddr),
		IsIdle:          true,
	}
}

// RegisterServer 注册服务
func RegisterServer(name string, server interface{}, conventions interface{}) error {
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

	svcMultiplexer = NewSvcMultiplexer(defaultRPCInstance, opts...)
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
