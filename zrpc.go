package zrpc

import (
	"github.com/panjf2000/ants/v2"
	"github.com/pborman/uuid"
)

var DefaultNode = Node{
	ServiceName:     getServerName(),
	NodeID:          uuid.NewUUID().String(),
	LocalEndpoint:   "tcp://0.0.0.0:8080",
	ClusterEndpoint: "tcp://0.0.0.0:8081",
	StateEndpoint:   "tcp://0.0.0.0:8082",
	IsIdle:          true,
}

var (
	defaultLogger Logger

	defaultRPCInstance *RPCInstance

	goroutinePool *ants.Pool

	svcMultiplexer *SvcMultiplexer
)

// SetLogger 设置默认 logger
func SetLogger(l Logger) {
	defaultLogger = l
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
func Run() error {
	if goroutinePool == nil {
		if err := SetWorkPoolSize(0); err != nil {
			return err
		}
	}

	if defaultRPCInstance == nil {
		defaultRPCInstance = NewRPCInstance()
	}

	if defaultLogger != nil {
		svcMultiplexer = NewSvcMultiplexer(defaultRPCInstance, WithLogger(defaultLogger))
	} else {
		svcMultiplexer = NewSvcMultiplexer(defaultRPCInstance)
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
