package zrpc

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"syscall"

	"github.com/vmihailenco/msgpack/v5"
)

type IMethodFunc interface {
	Call(params []msgpack.RawMessage)
}

type IReply interface {
	Reply(p *Pack) error
}

var _ IMethodFunc = (*ReqRepFunc)(nil)

type ReqRepFunc struct {
	id     string
	Method reflect.Value   // function
	Params []reflect.Value // params
	reply  IReply
}

func NewReqRepFunc(p *Pack, m *Method, r IReply) (IMethodFunc, error) {

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	var ctx *Context
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		return nil, fmt.Errorf("zrpc: %s", err.Error())
	}
	paramsValue[0] = reflect.ValueOf(ctx)
	for i := 1; i < len(p.Args); i++ {
		fieldType := m.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			return nil, err
		}
		paramsValue[i] = fieldValue
	}

	return &ReqRepFunc{
		id:     p.Identity,
		Method: m.method,
		Params: paramsValue,
		reply:  r,
	}, nil
}

func (f *ReqRepFunc) Call([]msgpack.RawMessage) {
	var rets []msgpack.RawMessage
	for _, item := range f.Method.Call(f.Params) {
		ret, _ := msgpack.Marshal(item.Interface())
		rets = append(rets, ret)
	}

	resp := &Pack{
		Identity:   f.id,
		MethodName: REPLY,
		Args:       rets,
	}
	f.reply.Reply(resp)
}


type StreamReqRepFunc struct {
	Method reflect.Value // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewStreamReqRepFunc(p *Pack, m *Method, r IReply) IMethodFunc {
	blockSize, _ := strconv.Atoi(p.Get(BLOCKSIZE))
	// TODO buf 大小
	rw := NewRWChannel(blockSize)

	return &StreamReqRepFunc{
		Method: m.method,
		reply:  r,
		rw:     rw,
		buf:    bufio.NewReadWriter(bufio.NewReader(rw), bufio.NewWriter(rw)),
	}
}

func (f *StreamReqRepFunc) Call(params []msgpack.RawMessage) {
	// TODO 参数：ctx, ..., reader
	f2, _ := os.OpenFile("test.txt", 0666, syscall.O_WRONLY)

	f2.Close()

	//n, err := f2.Write([]byte("aaaa"))
}
