package zrpc

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

var _ IReply = (*SvcMultiplexer)(nil)

type SvcMultiplexer struct {
	activeChannels map[string]IMethodFunc
	nodeState      *NodeState
	broker         Broker
	rpc            *RPCInstance
	mutex          sync.RWMutex
}

func NewSvcMultiplexer(nodeState *NodeState, logger Logger) *SvcMultiplexer {
	broker, err := NewBroker(nodeState, 5*time.Second, logger)
	if err != nil {
		panic(err)
	}
	return &SvcMultiplexer{
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

func (m *SvcMultiplexer) submitTask(f func()) {
	m.nodeState.gpool.Submit(f)
}

func (m *SvcMultiplexer) dispatcher(ctx context.Context) {
	for pack := range m.broker.NewTask() {
		msgid := pack.Get(MESSAGEID)
		if msgid == "" {
			m.SendError(pack.Identity, ErrNoMessageID)
			continue
		}
		switch pack.Stage {
		case REQUEST: // 请求
			if m.nodeState.isIdle() {
				mf, err := m.rpc.GenerateExecFunc(ctx, pack.MethodName())
				if err != nil {
					m.SendError(pack.Identity, err)
					continue
				}

				if mf.FuncMode() == ReqRep {
					m.submitTask(func() {
						mf.Call(pack, m)
					})
				} else {
					m.mutex.Lock()
					m.activeChannels[msgid] = mf
					m.mutex.Unlock()
					m.submitTask(func() {
						mf.Call(pack, m)
					})
				}
			} else {
				
			}
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

func (m *SvcMultiplexer) Run(ctx context.Context) {
	go m.broker.Run()
	m.dispatcher(ctx)
}

func (m *SvcMultiplexer) Close() {
	m.broker.Close(nil) // TODO
}
