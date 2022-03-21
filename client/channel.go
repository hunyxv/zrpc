package client

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		return newStreamReqRepChannel(base), nil
	case zrpc.ReqStreamRep:
		return newReqStreamRepChannel(base), nil
	case zrpc.Stream:
		return newStreamChannel(base), nil
	}
	return nil, fmt.Errorf("zrpc-cli: unknown function type: %+v", m.mode)
}

type methodChannel interface {
	MsgID() string
	Call(args []reflect.Value) []reflect.Value
	Receive(p *zrpc.Pack)
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

func (c *_methodChannel) marshalParams(args []reflect.Value) (ctx context.Context, params [][]byte, err error) {
	ctx = args[0].Interface().(context.Context)
	binCtx, err := msgpack.Marshal(&zrpc.Context{Context: ctx})
	if err != nil {
		return
	}
	params = append(params, binCtx)

	for i := 1; i < len(args)-1; i++ {
		binArg, err := msgpack.Marshal(args[i].Interface())
		if err != nil {
			return ctx, nil, err
		}
		params = append(params, binArg)
	}

	if len(args) > 1 {
		if c.method.mode == zrpc.ReqRep {
			binArg, err := msgpack.Marshal(args[len(args)-1].Interface())
			if err != nil {
				return ctx, nil, err
			}
			params = append(params, binArg)
		} else {
			// 其他传递 空 占位
			params = append(params, []byte{})
		}
	}
	return
}

func (c *_methodChannel) unmarshalResult(rets [][]byte) (results []reflect.Value, err error) {
	for i := 0; i < len(rets)-1; i++ {
		var ret = reflect.New(c.method.resultTypes[i])
		err = msgpack.Unmarshal(rets[i], ret.Interface())
		if err != nil {
			return
		}
		results = append(results, ret.Elem())
	}

	// 最后一个 err
	var errStr string
	err = msgpack.Unmarshal(rets[len(rets)-1], &errStr)
	if err != nil {
		return
	}
	if errStr != "" {
		results = append(results, reflect.ValueOf(errors.New(errStr)))
	} else {
		results = append(results, reflect.New(c.method.resultTypes[len(c.method.resultTypes)-1]).Elem())
	}
	return
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

		ch: make(chan *zrpc.Pack, 1),
	}
}

func (rr *reqRepChannel) Call(args []reflect.Value) []reflect.Value {
	defer rr._methodChannel.release()
	defer close(rr.ch)

	ctx, params, err := rr.marshalParams(args)
	if err != nil {
		return rr.errResult(err)
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

		results, err := rr.unmarshalResult(retPack.Args)
		if err != nil {
			return rr.errResult(err)
		}
		return results
	}
}
func (rr *reqRepChannel) Receive(p *zrpc.Pack) {
	rr.ch <- p
}

type streamReqRepChannel struct {
	*_methodChannel

	ch chan *zrpc.Pack
}

func newStreamReqRepChannel(base *_methodChannel) *streamReqRepChannel {
	return &streamReqRepChannel{
		_methodChannel: base,

		ch: make(chan *zrpc.Pack, 1),
	}
}

func (sr *streamReqRepChannel) Call(args []reflect.Value) []reflect.Value {
	defer sr._methodChannel.release()
	defer close(sr.ch)

	var reader io.Reader
	reader, ok := args[len(args)-1].Interface().(io.Reader)
	if !ok {
		return sr.errResult(fmt.Errorf(
			"cannot use '%s' as io.Reader value in argument to %s",
			args[len(args)-1].Type().Kind(), sr.method.methodName))
	}

	ctx, params, err := sr.marshalParams(args)
	if err != nil {
		return sr.errResult(err)
	}

	pack := &zrpc.Pack{
		Stage: zrpc.REQUEST,
		Args:  params,
	}
	pack.Set(zrpc.MESSAGEID, sr.MsgID())
	pack.SetMethodName(sr.method.methodName)
	nid, err := sr.manager.Send(pack)
	if err != nil {
		return sr.errResult(err)
	}

	var buf [4096]byte
	var eof bool
	tmpCh := make(chan struct{}, 1)
	defer close(tmpCh)
	tmpCh <- struct{}{}

	for {
		select {
		case <-ctx.Done():
			return sr.errResult(ctx.Err())
		case retPack := <-sr.ch:
			if retPack.Stage == zrpc.ERROR {
				var errStr string
				msgpack.Unmarshal(retPack.Args[0], &errStr)
				return sr.errResult(errors.New(errStr))
			}

			results, err := sr.unmarshalResult(retPack.Args)
			if err != nil {
				return sr.errResult(err)
			}
			return results
		case <-tmpCh:
			n, err := reader.Read(buf[:])
			if err != nil {
				if err != io.EOF {
					pack.Stage = zrpc.STREAM_END
					pack.Args = nil
					err = sr.manager.SpecifySend(nid, pack)
					if err != nil {
						return sr.errResult(err)
					}
					return sr.errResult(err)
				} else {
					eof = true
					pack.Stage = zrpc.STREAM_END
					pack.Args = nil
					err = sr.manager.SpecifySend(nid, pack)
					if err != nil {
						return sr.errResult(err)
					}
					break
				}
			}
			pack.Stage = zrpc.STREAM
			pack.Args = [][]byte{buf[:n]}
			err = sr.manager.SpecifySend(nid, pack)
			if err != nil {
				return sr.errResult(err)
			}
		}
		if !eof {
			tmpCh <- struct{}{}
		}
	}
}

func (sr *streamReqRepChannel) Receive(p *zrpc.Pack) {
	sr.ch <- p
}

type reqStreamRepChannel struct {
	*_methodChannel

	ch chan *zrpc.Pack
}

func newReqStreamRepChannel(base *_methodChannel) *reqStreamRepChannel {
	return &reqStreamRepChannel{
		_methodChannel: base,

		ch: make(chan *zrpc.Pack, 1),
	}
}

func (rs *reqStreamRepChannel) Call(args []reflect.Value) []reflect.Value {
	defer rs._methodChannel.release()
	defer close(rs.ch)

	var writeCloser io.WriteCloser
	writeCloser, ok := args[len(args)-1].Interface().(io.WriteCloser)
	if !ok {
		return rs.errResult(fmt.Errorf(
			"cannot use '%s' as io.WriterCloser value in argument to %s",
			args[len(args)-1].Type().Kind(), rs.method.methodName))
	}

	ctx, params, err := rs.marshalParams(args)
	if err != nil {
		return rs.errResult(err)
	}

	pack := &zrpc.Pack{
		Stage: zrpc.REQUEST,
		Args:  params,
	}
	pack.Set(zrpc.MESSAGEID, rs.MsgID())
	pack.SetMethodName(rs.method.methodName)
	_, err = rs.manager.Send(pack)
	if err != nil {
		return rs.errResult(err)
	}

	for {
		select {
		case <-ctx.Done():
			return rs.errResult(ctx.Err())
		case retPack := <-rs.ch:
			switch retPack.Stage {
			case zrpc.ERROR:
				var errStr string
				msgpack.Unmarshal(retPack.Args[0], &errStr)
				return rs.errResult(errors.New(errStr))
			case zrpc.STREAM:
				data := retPack.Args[0]
				var count = 0
				for count < len(data) {
					n, err := writeCloser.Write(data[count:])
					if err != nil {
						return rs.errResult(err)
					}
					count += n
				}
			case zrpc.STREAM_END:
				writeCloser.Close()
			case zrpc.REPLY:
				results, err := rs.unmarshalResult(retPack.Args)
				if err != nil {
					return rs.errResult(err)
				}
				return results
			}
		}
	}
}

func (rs *reqStreamRepChannel) Receive(p *zrpc.Pack) {
	rs.ch <- p
}

type sendItem struct {
	p   *zrpc.Pack
	err error
}

type streamChannel struct {
	*_methodChannel

	ch     chan *zrpc.Pack
	sendCh chan *sendItem
}

func newStreamChannel(base *_methodChannel) *streamChannel {
	return &streamChannel{
		_methodChannel: base,

		ch:     make(chan *zrpc.Pack, 1),
		sendCh: make(chan *sendItem),
	}
}

func (s *streamChannel) Call(args []reflect.Value) []reflect.Value {
	defer s._methodChannel.release()
	defer close(s.ch)
	defer close(s.sendCh)

	var readWriterCloser io.ReadWriteCloser
	readWriterCloser, ok := args[len(args)-1].Interface().(io.ReadWriteCloser)
	if !ok {
		return s.errResult(fmt.Errorf(
			"cannot use '%s' as io.ReadWriteCloser value in argument to %s",
			args[len(args)-1].Type().Kind(), s.method.methodName))
	}

	ctx, params, err := s.marshalParams(args)
	if err != nil {
		return s.errResult(err)
	}

	pack := &zrpc.Pack{
		Stage: zrpc.REQUEST,
		Args:  params,
	}
	pack.Set(zrpc.MESSAGEID, s.MsgID())
	pack.SetMethodName(s.method.methodName)
	nid, err := s.manager.Send(pack)
	if err != nil {
		return s.errResult(err)
	}

	go s.read(ctx, readWriterCloser)
	for {
		select {
		case <-ctx.Done():
			return s.errResult(ctx.Err())
		case retPack := <-s.ch:
			switch retPack.Stage {
			case zrpc.ERROR:
				var errStr string
				msgpack.Unmarshal(retPack.Args[0], &errStr)
				return s.errResult(errors.New(errStr))
			case zrpc.STREAM:
				data := retPack.Args[0]
				var count = 0
				for count < len(data) {
					n, err := readWriterCloser.Write(data[count:])
					if err != nil {
						return s.errResult(err)
					}
					count += n
				}
			case zrpc.STREAM_END:
				readWriterCloser.Close()
			case zrpc.REPLY:
				results, err := s.unmarshalResult(retPack.Args)
				if err != nil {
					return s.errResult(err)
				}
				return results
			}
		case sendData := <-s.sendCh:
			if sendData.err != nil {
				pack.Stage = zrpc.STREAM_END
				pack.Args = nil
				if err := s.manager.SpecifySend(nid, pack); err != nil {
					return s.errResult(err)
				}
				if sendData.err != io.EOF {
					return s.errResult(err)
				}
			} else {
				err = s.manager.SpecifySend(nid, sendData.p)
				if err != nil {
					return s.errResult(err)
				}
			}
		}
	}
}

func (s *streamChannel) read(ctx context.Context, r io.Reader) {
	var buf [4096]byte
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := r.Read(buf[:])
			if err != nil {
				s.sendCh <- &sendItem{
					err: err,
				}
				return
			}

			tmp := make([]byte, n)
			copy(tmp, buf[:n])
			pack := &zrpc.Pack{
				Stage: zrpc.STREAM,
				Args:  [][]byte{tmp},
			}
			pack.Set(zrpc.MESSAGEID, s.MsgID())
			pack.SetMethodName(s.method.methodName)
			s.sendCh <- &sendItem{
				p: pack,
			}
		}
	}
}

func (s *streamChannel) Receive(p *zrpc.Pack) {
	s.ch <- p
}
