package zrpc

import (
	"context"
	"log"
	"sync"
	"time"
)

type SvcMultiplexer struct {
	activeChannels map[string]IMethodFunc
	broker         Broker
	rpc            *RPCInstance
	mutex          sync.RWMutex
}

func NewSvcMultiplexer(nodeState *NodeState) *SvcMultiplexer {
	broker, err := NewBroker(nodeState, 5*time.Second)
	if err != nil {
		panic(err)
	}
	return &SvcMultiplexer{
		activeChannels: map[string]IMethodFunc{},
		broker:         broker,
		rpc:            DefaultRPCInstance,
	}
}

func (m *SvcMultiplexer) Send(p *Pack) error {
	return m.broker.Reply(p)
}

func (m *SvcMultiplexer) Reply(p *Pack) error {
	m.mutex.Lock()
	delete(m.activeChannels, p.Identity)
	m.mutex.Unlock()
	return m.broker.Reply(p)
}

func (m *SvcMultiplexer) Dispatcher(ctx context.Context) {
	for pack := range m.broker.NewTask() {
		id := pack.Identity
		if mf, ok := m.activeChannels[id]; ok {
			mf.Next(pack.Args)
		} else {
			mf, err := m.rpc.GenerateExecFunc(ctx, pack.MethodName)
			if err != nil {
				// TODO 发生 error
				log.Print(err)
			}
			m.activeChannels[id] = mf
			mf.Call(pack, m)
		}
	}
}

func (m *SvcMultiplexer) Run(ctx context.Context) {
	go m.broker.Run()
	m.Dispatcher(ctx)
}

func (m *SvcMultiplexer) Close() {
	m.broker.Close(nil) // TODO
}
