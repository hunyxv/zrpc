package zrpc

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/hunyxv/utils/timer"
	"github.com/panjf2000/ants/v2"
	"github.com/vmihailenco/msgpack/v5"
)

var _ IReply = (*SvcMultiplexer)(nil)

type SvcMultiplexer struct {
	logger         Logger
	activeChannels *activeMethodFuncs
	nodeState      *NodeState
	broker         Broker
	rpc            *RPCInstance
	timer          *timer.HashedWheelTimer
	forward        *myMap
	c              chan int
}

func NewSvcMultiplexer(nodeState *NodeState, logger Logger) *SvcMultiplexer {
	broker, err := NewBroker(nodeState, 5*time.Second, logger)
	if err != nil {
		panic(err)
	}

	t, err := timer.NewHashedWheelTimer(context.Background(), timer.WithWorkPool(nodeState.gpool))
	if err != nil {
		panic(err)
	}
	mux := &SvcMultiplexer{
		logger:         logger,
		activeChannels: newActiveMethodFuncs(t),
		nodeState:      nodeState,
		broker:         broker,
		rpc:            DefaultRPCInstance,
		timer:          t,
		forward:        newMyMap(t),
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
	errp.SetMethodName(ERROR)
	if err := m.Reply(errp); err != nil {
		log.Printf("reply message fail: %v", err)
	}
}

func (m *SvcMultiplexer) submitTask(f func()) error {
	if m.nodeState.gpool == nil {
		return ants.ErrPoolOverload
	}
	return m.nodeState.gpool.Submit(f)
}

func (m *SvcMultiplexer) do(msgid string, pack *Pack) error {
	mf, err := m.rpc.GenerateExecFunc(pack.MethodName())
	if err != nil {
		return err
	}

	if mf.FuncMode() == ReqRep {
		return m.submitTask(func() {
			mf.Call(pack, m)
		})
	}

	m.activeChannels.Store(msgid, mf)
	return m.submitTask(func() {
		mf.Call(pack, m)
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
				var fromPeerNode bool
				ttlStr := pack.Header.Get(TTL)
				if ttlStr != "" && ttlStr != "0" {
					fromPeerNode = true
				}

				// 不是来自平行节点或本节点空闲
				if !fromPeerNode || m.nodeState.isIdle() {
					err := m.do(msgid, pack)
					if err == nil {
						continue
					} else if !errors.Is(err, ants.ErrPoolOverload) {
						// 本地满载了，转发。报的不是满载异常，返回异常给客户端
						m.logger.Errorf("SvcMultiplexer: %v", err)
						m.SendError(pack, ErrSubmitTimeout)
						continue
					}
				}

				m.activeChannels.Delete(msgid)

				// 转发给其他节点
				n, err := m.SelectPeerNode()
				if err != nil {
					// 找不到其他空闲节点,就本节点处理
					m.logger.Warnf("SvcMultiplexer: %v", err)
					if err := m.do(msgid, pack); err != nil {
						// 本地无法处理报错
						m.logger.Errorf("SvcMultiplexer: %v", err)
						m.activeChannels.Delete(msgid)
						m.SendError(pack, ErrSubmitTimeout)
					}
					continue
				}
				m.broker.ForwardToPeerNode(n.NodeID, pack) // 转发
				m.forward.Store(msgid, n.NodeID)           // 保存消息和节点对应关系
			case STREAM: // 流式请求中
				mf, ok := m.activeChannels.Load(msgid)
				if ok {
					//m.submitTask(func() {
					if methodFunc, ok := mf.(IMethodFunc); ok {
						methodFunc.Next(pack.Args)
					}
					//})
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
						if methodFunc, ok := mf.(IMethodFunc); ok {
							methodFunc.End()
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

func (m *SvcMultiplexer) Run() {
	go m.timer.Start()
	go m.broker.Run()
	m.dispatcher()
}

func (m *SvcMultiplexer) Close() {
	m.timer.Stop()
	m.broker.Close(nil) // TODO
	close(m.c)
}

type activeMethodFuncs struct {
	sync.Map
	timer *timer.HashedWheelTimer
}

func newActiveMethodFuncs(t *timer.HashedWheelTimer) *activeMethodFuncs {
	return &activeMethodFuncs{
		timer: t,
	}
}

func (m *activeMethodFuncs) Store(key interface{}, value interface{}) {
	v := &_Value{
		v: value,
		t: m.timer.Submit(5*time.Minute, func() {
			if value, ok := m.Map.LoadAndDelete(key); ok {
				v := value.(*_Value)
				if f, ok := v.v.(IMethodFunc); ok {
					f.Release()
				}
			}
		}),
	}
	m.Map.Store(key, v)
}

func (m *activeMethodFuncs) Load(key interface{}) (interface{}, bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Reset()
	return value.v, true
}

func (m *activeMethodFuncs) LoadAndDelete(key interface{}) (interface{}, bool) {
	v, ok := m.Map.LoadAndDelete(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Cancel()
	return value.v, true
}

func (m *activeMethodFuncs) Delete(key interface{}) {
	m.LoadAndDelete(key)
}
