package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"

	"github.com/hunyxv/zrpc"
	"github.com/vmihailenco/msgpack/v5"
)

func newMethodChannle(m method, manager *channelManager) (methodChannel, error) {
	switch m.mode {
	case zrpc.ReqRep:
		return newReqRepChannel(m, manager), nil
	case zrpc.StreamReqRep:
		return nil, nil
	case zrpc.ReqStreamRep:
		return nil, nil
	case zrpc.Stream:
		return nil, nil
	}
	return nil, fmt.Errorf("zrpc-cli: unknown function type: %+v", m.mode)
}

type methodChannel interface {
	MsgID() string
	Call(args []reflect.Value) []reflect.Value
	Receive(p *zrpc.Pack)
}

type channels struct {
	m map[string]methodChannel // messageid:channel

	mutex sync.RWMutex
}

func (c *channels) insert(msgid string, ch methodChannel) {
	c.mutex.Lock()
	c.m[msgid] = ch
	c.mutex.Unlock()
}

func (c *channels) load(msgid string) (methodChannel, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	ch, ok := c.m[msgid]
	return ch, ok
}

func (c *channels) remove(msgid string) {
	c.mutex.Lock()
	delete(c.m, msgid)
	c.mutex.Unlock()
}

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

var _ methodChannel = (*reqRepChannel)(nil)

type reqRepChannel struct {
	msgid   string
	method  method
	ch      chan *zrpc.Pack
	manager *channelManager
}

func newReqRepChannel(m method, manager *channelManager) *reqRepChannel {
	return &reqRepChannel{
		msgid:   zrpc.NewMessageID(),
		method:  m,
		ch:      make(chan *zrpc.Pack),
		manager: manager,
	}
}

func (rr *reqRepChannel) errResult(err error) (results []reflect.Value) {
	for _, t := range rr.method.resultTypes[:len(rr.method.resultTypes)-1] {
		r := reflect.New(t).Elem()
		results = append(results, r)
	}
	results = append(results, reflect.ValueOf(err))
	return
}

func (rr *reqRepChannel) MsgID() string {
	if len(rr.msgid) == 0 {
		rr.msgid = zrpc.NewMessageID()
	}
	return rr.msgid
}

func (rr *reqRepChannel) Call(args []reflect.Value) []reflect.Value {
	var params [][]byte
	// 第一个参数为ctx
	ctx := args[0].Interface().(context.Context)
	binCtx, err := msgpack.Marshal(&zrpc.Context{Context: ctx})
	if err != nil {
		return rr.errResult(err)
	}

	params = append(params, binCtx)
	for i := 1; i < len(args); i++ {
		binArg, err := msgpack.Marshal(args[i].Interface())
		if err != nil {
			return rr.errResult(err)
		}
		params = append(params, binArg)
	}

	pack := &zrpc.Pack{
		Stage: zrpc.REQUEST,
		Args:  params,
	}
	pack.Set(zrpc.MESSAGEID, rr.MsgID())
	pack.SetMethodName(rr.method.methodName)
	_, err = rr.manager.Send(pack)
	if err != nil {
		return rr.errResult(err)
	}

	// TODO retry
	select {
	case <-ctx.Done():
		return rr.errResult(ctx.Err())
	case retPack := <-rr.ch:
		if retPack.Stage == zrpc.ERROR {
			var errStr string
			msgpack.Unmarshal(retPack.Args[0], &errStr)
			return rr.errResult(errors.New(errStr))
		}

		results := make([]reflect.Value, 0, len(rr.method.resultTypes))
		for i := 0; i < len(retPack.Args)-1; i++ {
			var ret = reflect.New(rr.method.resultTypes[i])
			err := msgpack.Unmarshal(retPack.Args[i], ret.Interface())
			if err != nil {
				return rr.errResult(err)
			}
			results = append(results, ret.Elem())
		}
		// 最后一个 err
		var errStr string
		err = msgpack.Unmarshal(retPack.Args[len(retPack.Args)-1], &errStr)
		if err != nil {
			return rr.errResult(err)
		}
		if errStr != "" {
			results = append(results, reflect.ValueOf(errors.New(errStr)))
		} else {
			results = append(results, reflect.New(rr.method.resultTypes[len(rr.method.resultTypes)-1]).Elem())
		}
		return results
	}
}
func (rr *reqRepChannel) Receive(p *zrpc.Pack) {
	rr.ch <- p
}
