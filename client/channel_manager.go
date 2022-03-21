package client

import (
	"log"
	"sync"

	"github.com/hunyxv/zrpc"
)

type channelManager struct {
	channels map[string]*sync.Map // method name: channels
	zconn    zConnecter
	c        chan struct{}

	mutex sync.RWMutex
}

func newChannelManager(conn zConnecter) *channelManager {
	manager := &channelManager{
		channels: make(map[string]*sync.Map),
		zconn:    conn,
		c:        make(chan struct{}),
	}

	return manager
}

func (manager *channelManager) start() {
	for {
		select {
		case <-manager.c:
			return
		case p := <-manager.zconn.Recv():
			manager.mutex.RLock()
			chs, ok := manager.channels[p.MethodName()]
			if !ok {
				log.Printf("[zrpc-cli]: returned result cannot find methodfunc")
				continue
			}
			manager.mutex.RUnlock()
			msgid := p.Get(zrpc.MESSAGEID)
			ch, ok := chs.Load(msgid)
			if !ok {
				log.Printf("[zrpc-cli]: returned result cannot find consumer")
				continue
			}

			ch.(methodChannel).Receive(p)
		}
	}
}

func (manager *channelManager) getChannels(methodName string) *sync.Map {
	manager.mutex.RLock()
	chs, ok := manager.channels[methodName]
	if !ok {
		manager.mutex.RUnlock()
		manager.mutex.Lock()
		chs, ok = manager.channels[methodName]
		if !ok {
			chs = new(sync.Map)
			manager.channels[methodName] = chs
		}
		manager.mutex.Unlock()
		return chs
	}

	manager.mutex.RUnlock()
	return chs
}

func (manager *channelManager) insertNewChannel(methodName string, ch methodChannel) {
	chs := manager.getChannels(methodName)
	chs.Store(ch.MsgID(), ch)
}

func (manager *channelManager) removeChannel(methodName string, msgid string) {
	chs := manager.getChannels(methodName)
	chs.Delete(msgid)
}

// Send 发送 pack
func (manager *channelManager) Send(p *zrpc.Pack) (string, error) {
	return manager.zconn.Send(p)
}

// SpecifySend 向指定服务节点发送 pack
func (manager *channelManager) SpecifySend(id string, p *zrpc.Pack) error {
	return manager.zconn.SpecifySend(id, p)
}

func (manager *channelManager) Recv(data []byte) {
	return
}

func (manager *channelManager) Close() {
	select {
	case <-manager.c:
	default:
		close(manager.c)
	}
	return
}
