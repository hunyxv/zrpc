package zrpc

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/vmihailenco/msgpack/v5"
)

var _ IReply = (*SvcMultiplexer)(nil)

type SvcMultiplexer struct {
	logger         Logger
	activeChannels map[string]IMethodFunc
	nodeState      *NodeState
	broker         Broker
	rpc            *RPCInstance
	mutex          sync.RWMutex
	forward        sync.Map
	i              int
}

func NewSvcMultiplexer(nodeState *NodeState, logger Logger) *SvcMultiplexer {
	broker, err := NewBroker(nodeState, 5*time.Second, logger)
	if err != nil {
		panic(err)
	}
	return &SvcMultiplexer{
		logger:         logger,
		activeChannels: map[string]IMethodFunc{},
		nodeState:      nodeState,
		broker:         broker,
		rpc:            DefaultRPCInstance,
	}
}

func (m *SvcMultiplexer) Reply(p *Pack) error {
	switch p.Stage {
	case REPLY: // 应答
	case STREAM: // 流式响应
		return m.broker.Reply(p)
	case STREAM_END: // 流式响应结束
	}
	msgid := p.Get(MESSAGEID)
	m.mutex.Lock()
	delete(m.activeChannels, msgid)
	m.mutex.Unlock()
	return m.broker.Reply(p)
}

func (m *SvcMultiplexer) SendError(id string, e error) {
	raw, _ := msgpack.Marshal(e.Error())
	errp := &Pack{
		Identity: id,
		Stage:    ERROR,
		Args:     []msgpack.RawMessage{raw},
	}
	errp.SetMethodName(ERROR)
	if err := m.Reply(errp); err != nil {
		log.Printf("reply message fail: %v", err)
	}
}

func (m *SvcMultiplexer) sendError(pack *Pack, e error) {
	id := pack.Identity
	if pid := pack.Header.Pop(PACKPATH); pid != "" {
		id = pid
	}

	m.SendError(id, e)
}

func (m *SvcMultiplexer) submitTask(f func()) error {
	return m.nodeState.gpool.Submit(f)
}

func (m *SvcMultiplexer) dispatcher(ctx context.Context) {
	for pack := range m.broker.NewTask() {
		msgid := pack.Get(MESSAGEID)
		if msgid == "" {
			m.sendError(pack, ErrNoMessageID)
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
				mf, err := m.rpc.GenerateExecFunc(ctx, pack.MethodName())
				if err != nil {
					m.sendError(pack, err)
					continue
				}

				if mf.FuncMode() == ReqRep {
					err = m.submitTask(func() {
						mf.Call(pack, m)
					})
				} else {
					m.mutex.Lock()
					m.activeChannels[msgid] = mf
					m.mutex.Unlock()
					err = m.submitTask(func() {
						mf.Call(pack, m)
					})
				}
				if err == nil {
					continue
				} else if !errors.Is(err, ants.ErrPoolOverload) {
					// 本地满载了，转发，报的不是满载异常，返回异常给客户端
					m.logger.Errorf("SvcMultiplexer: %v", err)
					m.sendError(pack, ErrSubmitTimeout)
					continue
				}
			}
			// 转发给其他节点
			n, err := m.SelectPeerNode()
			if err != nil {
				// 找不到空闲节点
				m.logger.Error("SvcMultiplexer: %v", err)
				m.sendError(pack, ErrSubmitTimeout)
				continue
			}
			m.broker.ForwardToPeerNode(n.NodeID, pack)
		case STREAM: // 流式请求中
			m.mutex.RLock()
			mf, ok := m.activeChannels[msgid]
			m.mutex.RUnlock()
			if ok {
				m.submitTask(func() {
					mf.Next(pack.Args)
				})
			} else {

			}
		case STREAM_END: // 流式请求结束
			m.mutex.RLock()
			mf, ok := m.activeChannels[msgid]
			m.mutex.RUnlock()
			if ok {
				m.submitTask(func() { mf.End() })
			}
		}
	}
}

func (m *SvcMultiplexer) SelectPeerNode() (n Node, err error) {
	nodes := m.broker.AllPeerNode()
	defer func() {
		m.i = (m.i + 1) % len(nodes)
	}()
	for j := 0; j < len(nodes); j++ {
		if nodes[m.i].IsIdle {
			return nodes[m.i], nil
		}
		m.i = (m.i + 1) % len(nodes)
	}
	err = errors.New("no idle nodes")
	return
}

func (m *SvcMultiplexer) Run(ctx context.Context) {
	go m.broker.Run()
	m.dispatcher(ctx)
}

func (m *SvcMultiplexer) Close() {
	m.broker.Close(nil) // TODO
}
