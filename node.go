package zrpc

import (
	"sync/atomic"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/vmihailenco/msgpack/v5"
)

var DefaultNodeState, _ = NewNodeState(&Node{
	ServiceName:     getServerName(),
	NodeID:          "",
	LocalEndpoint:   "tcp://0.0.0.0:8080",
	ClusterEndpoint: "tcp://0.0.0.0:8081",
	StateEndpoint:   "tcp://0.0.0.0:8082",
	IsIdle:          true,
}, 1000)

// Node 节点信息
type Node struct {
	ServiceName     string `json:"service_name" msgpack:"service_name"`
	NodeID          string `json:"nodeid" msgpack:"nodeid"`
	LocalEndpoint   string `json:"local_endpoint" msgpack:"local_endpoint"`     // 本地 endpoint
	ClusterEndpoint string `json:"cluster_endpoint" msgpack:"cluster_endpoint"` // 集群 endpoint
	StateEndpoint   string `json:"state_endpoint" msgpack:"state_endpoint"`     // 状态 endpoint
	IsIdle          bool   `json:"is_idle" msgpack:"is_idle"`
}

// type Metrics interface {
// 	Cap() int
// 	Running() int
// 	Submit(task func()) error
// }

// var _ Metrics = (*Workbench)(nil)

type NodeState struct {
	*Node

	gpool      *ants.Pool
	flag       int32     // 暂停工作
	expiration time.Time // 过期时间，通过心跳来更新
}

func NewNodeState(node *Node, gpoolSize int) (*NodeState, error) {
	pool, err := ants.NewPool(gpoolSize, ants.WithNonblocking(true))
	if err != nil {
		return nil, err
	}
	return &NodeState{
		Node:  node,
		gpool: pool,
	}, nil
}

func (s *NodeState) pause() {
	atomic.CompareAndSwapInt32(&(s.flag), 0, 1)
}

func (s *NodeState) pursue() {
	atomic.CompareAndSwapInt32(&(s.flag), 1, 0)
}

func (s *NodeState) isPausing() bool {
	return atomic.LoadInt32(&(s.flag)) == 1
}

func (s *NodeState) isIdle() bool {
	// 本节点
	if s.gpool != nil {
		// 本节点负载大于 80% 不再接收其他节点任务
		return float64(s.gpool.Running())/float64(s.gpool.Cap()) <= 0.9
	}

	// 平行节点
	return s.IsIdle
}

func (s *NodeState) Marshal() []byte {
	s.IsIdle = s.isIdle()
	b, _ := msgpack.Marshal(s.Node)
	return b
}

func (s *NodeState) Unmarshal(b []byte) {
	msgpack.Unmarshal(b, &(s.Node))
}
