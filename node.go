package zrpc

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type Endpoint struct {
	Scheme string
	Host   string
	Port   int
}

func (e Endpoint) String() string {
	return fmt.Sprintf("%s://%s:%d", e.Scheme, e.Host, e.Port)
}

// Node 节点信息
type Node struct {
	ServiceName     string   `json:"service_name" msgpack:"service_name"`
	NodeID          string   `json:"nodeid" msgpack:"nodeid"`
	LocalEndpoint   Endpoint `json:"local_endpoint" msgpack:"local_endpoint"`     // 本地 endpoint
	ClusterEndpoint Endpoint `json:"cluster_endpoint" msgpack:"cluster_endpoint"` // 集群 endpoint
	StateEndpoint   Endpoint `json:"state_endpoint" msgpack:"state_endpoint"`     // 状态 endpoint
	IsIdle          bool     `json:"is_idle" msgpack:"is_idle"`
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

func (s *NodeState) isIdle() (bool, bool) {
	// 本节点
	if s.isSelf {
		if goroutinePool == nil {
			return false, false
		}
		// 本节点负载大于 80% 不再接收其他节点任务
		idle := float64(goroutinePool.Running())/float64(goroutinePool.Cap()) <= 0.8
		if idle && s.isPausing() {
			s.pursue()
			return true, true
		}
		if !idle && !s.isPausing() {
			s.pause()
			return true, true
		}
		return idle, false
	}

	// 平行节点
	return s.IsIdle, false
}

func (s *NodeState) MarshalMsgpack() ([]byte, error) {
	s.IsIdle, _ = s.isIdle()
	node := *(s.Node)
	ips, err := getLocalIps()
	if err != nil {
		return nil, err
	}
	if len(ips) > 0 {
		node.LocalEndpoint.Host = ips[0]
		node.ClusterEndpoint.Host = ips[0]
		node.StateEndpoint.Host = ips[0]
	}
	return msgpack.Marshal(node)
}

func (s *NodeState) UnmarshalMsgpack(b []byte) error {
	return msgpack.Unmarshal(b, &(s.Node))
}

func (s *NodeState) MarshalJSON() ([]byte, error) {
	s.IsIdle, _ = s.isIdle()
	node := *(s.Node)
	ips, err := getLocalIps()
	if err != nil {
		return nil, err
	}
	if len(ips) > 0 {
		node.LocalEndpoint.Host = ips[0]
		node.ClusterEndpoint.Host = ips[0]
		node.StateEndpoint.Host = ips[0]
	}
	return json.Marshal(node)
}

func (s *NodeState) UnmarshalJSON(b []byte) error {
	return msgpack.Unmarshal(b, &(s.Node))
}
