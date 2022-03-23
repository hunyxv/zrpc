package zrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"time"

	"github.com/hunyxv/utils/timer"
	"github.com/panjf2000/ants/v2"
	"github.com/vmihailenco/msgpack/v5"
)

var _ iReply = (*SvcMultiplexer)(nil)

type SvcMultiplexer struct {
	logger         Logger
	activeChannels *activeMethodFuncs
	nodeState      *NodeState
	broker         Broker
	rpc            *RPCInstance
	timer          *timer.HashedWheelTimer
	forward        *myMap
	opts           *options
	c              chan int
}

func NewSvcMultiplexer(rpc *RPCInstance, opts ...Option) *SvcMultiplexer {
	defOpts := &options{
		MaxTimeoutPeriod:  5 * time.Minute,
		Logger:            &logger{},
		Node:              DefaultNode,
		HeartbeatInterval: 10 * time.Second,
		PackTTL:           1,
	}
	for _, f := range opts {
		f(defOpts)
	}

	nodeState := &NodeState{Node: &defOpts.Node}
	broker, err := NewBroker(nodeState, 5*time.Second, defOpts.Logger)
	if err != nil {
		panic(err)
	}

	t, err := timer.NewHashedWheelTimer(context.Background(), timer.WithWorkPool(goroutinePool))
	if err != nil {
		panic(err)
	}
	mux := &SvcMultiplexer{
		logger:         defOpts.Logger,
		activeChannels: newActiveMethodFuncs(t, defOpts.MaxTimeoutPeriod),
		nodeState:      nodeState,
		broker:         broker,
		rpc:            rpc,
		timer:          t,
		forward:        newMyMap(t, defOpts.MaxTimeoutPeriod),
		opts:           defOpts,
		c:              make(chan int),
	}
	return mux
}

// AddPeerNode 用于测试，后面删掉
func (m *SvcMultiplexer) AddPeerNode(n *Node) {
	m.broker.AddPeerNode(n)
}

func (m *SvcMultiplexer) Reply(p *Pack) error {
	switch p.Stage {
	case REPLY, ERROR: // 只要应答（无论有无发生异常），方法的生命周期都应该算是结束了
		msgid := p.Get(MESSAGEID)
		m.activeChannels.Delete(msgid)
		m.forward.Delete(msgid)
		return m.broker.Reply(p)
	case STREAM: // 流式响应
		return m.broker.Reply(p)
	case STREAM_END: // 流式响应结束
		return m.broker.Reply(p)
	}
	return nil
}

func (m *SvcMultiplexer) SendError(pack *Pack, e error) {
	errRaw, _ := msgpack.Marshal(e.Error())
	errp := &Pack{
		Identity: pack.Identity,
		Header:   pack.Header,
		Stage:    ERROR,
		Args:     [][]byte{errRaw},
	}
	// errp.SetMethodName(pack.MethodName())
	if err := m.Reply(errp); err != nil {
		log.Printf("reply message fail: %v", err)
	}
}

func (m *SvcMultiplexer) submitTask(f func()) error {
	if goroutinePool == nil {
		return ants.ErrPoolOverload
	}
	return goroutinePool.Submit(f)
}

func (m *SvcMultiplexer) do(msgid string, pack *Pack) (err error) {
	mf, err := m.rpc.GenerateExecFunc(pack.MethodName(), m)
	if err != nil {
		return err
	}

	if mf.FuncMode() == ReqRep {
		return m.submitTask(func() {
			mf.Call(pack)
		})
	}

	defer func() {
		if err != nil {
			m.activeChannels.Delete(msgid)
		}
	}()

	m.activeChannels.Store(msgid, mf)
	return m.submitTask(func() {
		mf.Call(pack)
	})
}

func (m *SvcMultiplexer) dispatcher() {
	for {
		select {
		case <-m.c:
			return
		case pack := <-m.broker.NewTask():
			msgid := pack.Get(MESSAGEID)
			if msgid == "" {
				m.SendError(pack, ErrNoMessageID)
				continue
			}
			switch pack.Stage {
			case REQUEST: // 请求
				//var fromPeerNode bool
				ttlStr := pack.Header.Get(TTL)
				if ttlStr != "" && ttlStr != "0" {
					ttl, _ := strconv.Atoi(ttlStr)
					if ttl > m.opts.PackTTL { // 超过了最大生存时间
						m.logger.Warnf("SvcMultiplexer: packet exceeds maximum ttl, %d", ttl)
						m.SendError(pack, ErrSubmitTimeout)
						continue
					}
					//fromPeerNode = true
				}
				isIdle, ok := m.nodeState.isIdle()
				if ok {
					// 同步节点状态
					go func() { m.broker.PublishNodeState() }()
				}

				// 本节点空闲
				if isIdle {
					err := m.do(msgid, pack)
					if err == nil {
						continue
					} else if errors.Is(err, ants.ErrPoolOverload) {
						// 工作池满载了
						m.logger.Warnf("SvcMultiplexer: %v", err)
						m.SendError(pack, ErrSubmitTimeout)
						continue
					} else { // 其他异常
						m.logger.Errorf("SvcMultiplexer: %v", err)
						m.SendError(pack, err)
						continue
					}
				}

				// 转发给其他节点
				n, err := m.SelectPeerNode()
				if err != nil {
					// 找不到其他空闲节点,就本节点处理
					m.logger.Warnf("SvcMultiplexer: %v", err)
					if err := m.do(msgid, pack); err != nil {
						// 本地无法处理报错
						m.logger.Errorf("SvcMultiplexer: %v", err)
						m.SendError(pack, ErrSubmitTimeout)
					}
					continue
				}
				m.broker.ForwardToPeerNode(n.NodeID, pack) // 转发
				m.forward.Store(msgid, n.NodeID)           // 保存消息和节点对应关系
			case STREAM: // 流式请求中
				mf, ok := m.activeChannels.Load(msgid)
				if ok {
					if methodFunc, ok := mf.(methodFunc); ok {
						methodFunc.Next(pack.Args)
					}
				} else {
					value, ok := m.forward.Load(msgid)
					if !ok {
						// 无处理节点，丢弃
						m.logger.Warnf("task processing node not found")
						continue
					}
					nodeid := value.(string)
					m.broker.ForwardToPeerNode(nodeid, pack)
				}
			case STREAM_END: // 流式请求结束
				mf, ok := m.activeChannels.LoadAndDelete(msgid)
				if ok {
					m.submitTask(func() {
						if methodFunc, ok := mf.(methodFunc); ok {
							methodFunc.End(nil)
						}
					})
				} else {
					value, ok := m.forward.Load(msgid)
					if !ok {
						// 无处理节点，丢弃
						m.logger.Warnf("stage %s: task processing node not found", STREAM_END)
						continue
					}
					nodeid := value.(string)
					m.broker.ForwardToPeerNode(nodeid, pack)
				}
			}
		}
	}
}

func (m *SvcMultiplexer) SelectPeerNode() (n Node, err error) {
	nodes := m.broker.AllPeerNode()
	if len(nodes) == 0 {
		err = errors.New("no idle nodes")
		return
	}

	i := rand.Intn(len(nodes))
	for j := 0; j < len(nodes); j++ {
		if nodes[i].IsIdle {
			return nodes[i], nil
		}
		i = (i + 1) % len(nodes)
	}
	err = errors.New("no idle nodes")
	return
}

func (m *SvcMultiplexer) AddOrUpdate(nodeid string, metadata []byte) error {
	if nodeid == m.nodeState.NodeID {
		return nil
	}

	var node Node
	if err := json.Unmarshal(metadata, &node); err != nil {
		return err
	}

	all := m.broker.AllPeerNode()
	var updateNode *Node
	for _, n := range all {
		if n == node {
			return nil
		}

		if n.NodeID == node.NodeID ||
			n.LocalEndpoint == node.LocalEndpoint ||
			n.ClusterEndpoint == node.LocalEndpoint ||
			n.ClusterEndpoint == node.ClusterEndpoint ||
			n.StateEndpoint == node.StateEndpoint {
			updateNode = &n
		}
	}
	if updateNode != nil {
		m.broker.DelPeerNode(updateNode)
	}
	m.broker.AddPeerNode(&node)
	return nil
}

func (m *SvcMultiplexer) Delete(nodeid string) {
	if m.nodeState.NodeID == nodeid {
		return
	}
	all := m.broker.AllPeerNode()
	for _, node := range all {
		if node.NodeID == nodeid {
			m.broker.DelPeerNode(&node)
		}
	}
}

func (m *SvcMultiplexer) Run() {
	go m.timer.Start()
	go m.broker.Run()
	if m.opts.RegisterDiscover != nil {
		// 注册服务
		go m.opts.RegisterDiscover.Register()
		// 服务发现
		go m.opts.RegisterDiscover.Watch(m)
	}
	m.dispatcher()
}

func (m *SvcMultiplexer) Close() {
	if m.opts.RegisterDiscover != nil {
		m.opts.RegisterDiscover.Deregister()
		m.opts.RegisterDiscover.Stop()
	}
	m.timer.Stop()
	m.broker.Close(nil) // TODO
	close(m.c)
}
