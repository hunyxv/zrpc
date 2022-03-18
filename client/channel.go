package client

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/hunyxv/zrpc"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	pool sync.Pool = sync.Pool{New: func() any {
		return &_methodChannel{}
	}}
)

func newMethodChannle(m *method, manager *channelManager) (methodChannel, error) {
	base := pool.Get().(*_methodChannel)
	base.init(m, manager)
	switch m.mode {
	case zrpc.ReqRep:
		return newReqRepChannel(base), nil
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

type _methodChannel struct {
	msgid   string
	method  *method
	manager *channelManager
}

func (c *_methodChannel) init(m *method, manager *channelManager) {
	c.msgid = zrpc.NewMessageID()
	c.method = m
	c.manager = manager
}

func (c *_methodChannel) errResult(err error) (results []reflect.Value) {
	for _, t := range c.method.resultTypes[:len(c.method.resultTypes)-1] {
		r := reflect.New(t).Elem()
		results = append(results, r)
	}
	results = append(results, reflect.ValueOf(err))
	return
}

func (c *_methodChannel) MsgID() string {
	if len(c.msgid) == 0 {
		c.msgid = zrpc.NewMessageID()
	}
	return c.msgid
}

func (c *_methodChannel) release() {
	pool.Put(c)
}

var _ methodChannel = (*reqRepChannel)(nil)

type reqRepChannel struct {
	*_methodChannel
	ch chan *zrpc.Pack
}

func newReqRepChannel(base *_methodChannel) *reqRepChannel {
	return &reqRepChannel{
		_methodChannel: base,
		ch:             make(chan *zrpc.Pack, 1),
	}
}

func (rr *reqRepChannel) Call(args []reflect.Value) []reflect.Value {
	defer rr._methodChannel.release()
	defer close(rr.ch)

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
