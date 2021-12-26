package zrpc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"syscall"

	"github.com/vmihailenco/msgpack/v5"
)

type IMethodFunc interface {
	Call(p *Pack, r IReply)
	Next(params []msgpack.RawMessage)
}

type IReply interface {
	Send(p *Pack) error
	Reply(p *Pack) error
}

func NewMethodFunc(method *Method) (IMethodFunc, error) {
	switch method.mode {
	case ReqRep:
		return NewReqRepFunc(method)
	case StreamReqRep:
		return NewStreamReqRepFunc(method)
	case ReqStreamRep:
		return NewReqStreamRepFunc(method)
	case Stream:
		return NewStreamFunc(method)
	}
	return nil, errors.New("xxx")
}

var _ IMethodFunc = (*ReqRepFunc)(nil)

type ReqRepFunc struct {
	id string
	// header Header
	Method *Method // function
	reply  IReply
}

func NewReqRepFunc(m *Method) (IMethodFunc, error) {
	return &ReqRepFunc{
		Method: m,
	}, nil
}

func (f *ReqRepFunc) sendErr(e error) {
	rawerr, _ := msgpack.Marshal(e)
	pack := &Pack{
		Identity:   f.id,
		MethodName: ERROR,
		Args:       []msgpack.RawMessage{[]byte(""), rawerr},
	}

	f.reply.Reply(pack)
}

// Call 将 func 放入 pool 中运行
func (f *ReqRepFunc) Call(p *Pack, r IReply) {
	if len(p.Args) != len(f.Method.paramTypes) {
		f.sendErr(fmt.Errorf("not enough arguments in call to %s", f.Method.methodName))
		return
	}

	f.reply = r
	f.id = p.Identity
	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// paramsValue[0] = f.Method.srv ??
	var ctx *Context
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		// TODO 异常处理
		log.Println("err: ", err)
		f.sendErr(err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(f.Method.paramTypes); i++ {
		fieldType := f.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			// TODO 异常处理
			return
		}
		paramsValue[i+1] = fieldValue
	}

	var rets []msgpack.RawMessage
	for _, item := range f.Method.method.Call(paramsValue) {
		ret, _ := msgpack.Marshal(item.Interface())
		rets = append(rets, ret)
	}

	resp := &Pack{
		Identity:   p.Identity,
		MethodName: REPLY,
		Args:       rets,
	}
	f.reply.Reply(resp)
}

func (f *ReqRepFunc) Next([]msgpack.RawMessage) {}

type StreamReqRepFunc struct {
	Method reflect.Value // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewStreamReqRepFunc(m *Method) (IMethodFunc, error) {
	// blockSize, _ := strconv.Atoi(p.Get(BLOCKSIZE))
	// // TODO buf 大小
	// rw := NewRWChannel(blockSize)

	return &StreamReqRepFunc{
		Method: m.method,
		// rw:     rw,
		// buf:    bufio.NewReadWriter(bufio.NewReader(rw), bufio.NewWriter(rw)),
	}, nil
}

func (srf *StreamReqRepFunc) Call(p *Pack, r IReply) {
	// TODO 参数：ctx, ..., reader
	f2, _ := os.OpenFile("test.txt", 0666, syscall.O_WRONLY)

	f2.Close()

	//n, err := f2.Write([]byte("aaaa"))
}

func (srf *StreamReqRepFunc) Next([]msgpack.RawMessage) {
	// TODO
}

type ReqStreamRepFunc struct {
	Method reflect.Value // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewReqStreamRepFunc(m *Method) (IMethodFunc, error) {
	// TODO
	return nil, nil
}

func (rsf *ReqStreamRepFunc) Call(p *Pack, r IReply) {
	// TODO 参数：ctx, ..., reader
	f2, _ := os.OpenFile("test.txt", 0666, syscall.O_WRONLY)

	f2.Close()

	//n, err := f2.Write([]byte("aaaa"))
}

func (rsf *ReqStreamRepFunc) Next([]msgpack.RawMessage) {
	// TODO
}

type StreamFunc struct {
	Method reflect.Value // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewStreamFunc(m *Method) (IMethodFunc, error) {
	// TODO
	return nil, nil
}

func (sf *StreamFunc) Call(p *Pack, r IReply) {
	// TODO
}

func (sf *StreamFunc) Next([]msgpack.RawMessage) {
	// TODO
}
