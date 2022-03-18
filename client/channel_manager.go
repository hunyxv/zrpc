package client

import (
	"log"
	"sync"

	"github.com/hunyxv/zrpc"
)

type channelManager struct {
	channels map[string]*channels // method name: channels
	zconn    zConnecter
	c        chan struct{}

	mutex sync.RWMutex
}

func newChannelManager(conn zConnecter) *channelManager {
	manager := &channelManager{
		channels: make(map[string]*channels),
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
			ch, ok := chs.load(msgid)
			if !ok {
				log.Printf("[zrpc-cli]: returned result cannot find consumer")
				continue
			}

			ch.Receive(p)
		}
	}
}

func (manager *channelManager) getChannels(methodName string) *channels {
	manager.mutex.RLock()
	chs, ok := manager.channels[methodName]
	if !ok {
		manager.mutex.RUnlock()
		manager.mutex.Lock()
		chs, ok = manager.channels[methodName]
		if !ok {
			chs = &channels{m: make(map[string]methodChannel)}
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
	chs.insert(ch.MsgID(), ch)
}

func (manager *channelManager) removeChannel(methodName string, msgid string) {
	chs := manager.getChannels(methodName)
	chs.remove(msgid)
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
	close(manager.c)
	return
}
