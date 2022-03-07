package zrpc

import (
	"sync/atomic"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// Node 节点信息
type Node struct {
	ServiceName     string `json:"service_name" msgpack:"service_name"`
	NodeID          string `json:"nodeid" msgpack:"nodeid"`
	LocalEndpoint   string `json:"local_endpoint" msgpack:"local_endpoint"`     // 本地 endpoint
	ClusterEndpoint string `json:"cluster_endpoint" msgpack:"cluster_endpoint"` // 集群 endpoint
	StateEndpoint   string `json:"state_endpoint" msgpack:"state_endpoint"`     // 状态 endpoint
	IsIdle          bool   `json:"is_idle" msgpack:"is_idle"`
}

type NodeState struct {
	*Node

	isSelf     bool
	flag       int32     // 暂停工作
	expiration time.Time // 过期时间，通过心跳来更新
}

func NewNodeState(node *Node, workPoolSize int) (*NodeState, error) {
	if goroutinePool == nil {
		if err := SetWorkPoolSize(workPoolSize); err != nil {
			return nil, err
		}
	}

	return &NodeState{
		Node:   node,
		isSelf: true,
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
	if s.isSelf {
		// 本节点负载大于 80% 不再接收其他节点任务
		return float64(goroutinePool.Running())/float64(goroutinePool.Cap()) <= 0.8
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
