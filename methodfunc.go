package zrpc

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"reflect"

	"github.com/vmihailenco/msgpack/v5"
)

type IMethodFunc interface {
	FuncMode() FuncMode
	Call(p *Pack, r IReply)
	Next(params []msgpack.RawMessage)
	End()
}

type IReply interface {
	Reply(p *Pack) error
	SendError(identity string, err error)
}

func NewMethodFunc(method *Method) (IMethodFunc, error) {
	switch method.mode {
	case ReqRep:
		return NewReqRepFunc(method), nil
	case StreamReqRep:
		return NewStreamReqRepFunc(method), nil
	case ReqStreamRep:
		return NewReqStreamRepFunc(method), nil
	case Stream:
		return NewStreamFunc(method), nil
	}
	return nil, fmt.Errorf("unknown function type: %+v", method.mode)
}

var _ IMethodFunc = (*ReqRepFunc)(nil)

// ReqRepFunc 请求应答类型函数
type ReqRepFunc struct {
	id     string
	Method *Method // function
	reply  IReply
}

func NewReqRepFunc(m *Method) IMethodFunc {
	return &ReqRepFunc{
		Method: m,
	}
}

func (f *ReqRepFunc) FuncMode() FuncMode {
	return f.Method.mode
}

// Call 将 func 放入 pool 中运行
func (f *ReqRepFunc) Call(p *Pack, r IReply) {
	f.reply = r
	f.id = p.Identity

	if len(p.Args) != len(f.Method.paramTypes) {
		f.reply.SendError(f.id, fmt.Errorf("not enough arguments in call to %s", f.Method.methodName))
		return
	}

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// paramsValue[0] = f.Method.srv ??
	var ctx *Context
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		log.Println("err: ", err)
		f.reply.SendError(f.id, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(f.Method.paramTypes); i++ {
		fieldType := f.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			f.reply.SendError(f.id, err)
			return
		}
		paramsValue[i] = fieldValue
	}

	var rets []msgpack.RawMessage
	result := f.Method.method.Call(paramsValue)

	for _, item := range result[:len(result)-1] {
		ret, _ := msgpack.Marshal(item.Interface())
		rets = append(rets, ret)
	}

	// 最后一个是error类型
	errVal := result[len(result)-1]
	if !errVal.IsNil() {
		ret, _ := msgpack.Marshal(errVal.Interface().(error).Error())
		rets = append(rets, ret)
	} else {
		ret, _ := msgpack.Marshal(errVal.Interface())
		rets = append(rets, ret)
	}

	resp := &Pack{
		Identity: p.Identity,
		Stage:    REPLY,
		Args:     rets,
	}
	resp.SetMethodName(p.MethodName())
	f.reply.Reply(resp)
}

func (f *ReqRepFunc) Next([]msgpack.RawMessage) {}
func (f *ReqRepFunc) End()                      {}

// StreamReqRepFunc 流式请求类型函数
type StreamReqRepFunc struct {
	id     string
	Method *Method // function
	reply  IReply
	rw     *rwchannel
	buf    *bufio.ReadWriter
}

func NewStreamReqRepFunc(m *Method) IMethodFunc {
	return &StreamReqRepFunc{
		Method: m,
	}
}

func (srf *StreamReqRepFunc) FuncMode() FuncMode {
	return srf.Method.mode
}

func (srf *StreamReqRepFunc) Call(p *Pack, r IReply) {
	srf.id = p.Identity
	srf.reply = r

	if len(p.Args) != len(srf.Method.paramTypes) {
		srf.reply.SendError(srf.id, fmt.Errorf("not enough arguments in call to %s", srf.Method.methodName))
		return
	}
	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// 第一个参数是 ctx
	var ctx *Context
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		// TODO 异常处理
		log.Println("err: ", err)
		srf.reply.SendError(srf.id, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(srf.Method.paramTypes)-1; i++ {
		fieldType := srf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			// TODO 异常处理
			return
		}
		paramsValue[i] = fieldValue
	}
	// 最后一个是 ReadWriter
	srf.rw = NewRWChannel(0)
	srf.buf = bufio.NewReadWriter(bufio.NewReader(srf.rw), bufio.NewWriter(srf.rw))
	rwvalue := reflect.ValueOf(srf.buf)

	paramsValue[len(srf.Method.paramTypes)-1] = rwvalue

	// go func() {
	var rets []msgpack.RawMessage
	// 调用函数
	result := srf.Method.method.Call(paramsValue)

	for _, item := range result[:len(result)-1] {
		ret, _ := msgpack.Marshal(item.Interface())
		rets = append(rets, ret)
	}

	// 最后一个是error类型
	errVal := result[len(result)-1]
	if !errVal.IsNil() {
		ret, _ := msgpack.Marshal(errVal.Interface().(error).Error())
		rets = append(rets, ret)
	} else {
		ret, _ := msgpack.Marshal(errVal.Interface())
		rets = append(rets, ret)
	}

	resp := &Pack{
		Identity: p.Identity,
		Stage:    REPLY,
		Args:     rets,
	}
	resp.SetMethodName(p.MethodName())
	srf.reply.Reply(resp)
	// }()
}

func (srf *StreamReqRepFunc) Next(data []msgpack.RawMessage) {
	var tmp string
	err := msgpack.Unmarshal(data[0], &tmp) // TODO 这样弄不好，待优化
	if err != nil {
		panic(err)
	}
	raw := []byte(tmp)
	var i int
	for i != len(raw) {
		n, err := srf.buf.Write(raw[i:])
		if err != nil && err != io.EOF {
			srf.reply.SendError(srf.id, err)
			return
		}
		i += n
	}
	srf.buf.Flush()
}

func (srf *StreamReqRepFunc) End() {
	srf.rw.Close()
}

type ReqStreamRepFunc struct {
	Method *Method // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewReqStreamRepFunc(m *Method) IMethodFunc {
	// TODO
	return nil
}

func (rsf *ReqStreamRepFunc) FuncMode() FuncMode {
	return rsf.Method.mode
}

func (rsf *ReqStreamRepFunc) Call(p *Pack, r IReply) {

}

func (rsf *ReqStreamRepFunc) Next([]msgpack.RawMessage) {
	// TODO
}

func (rsf *ReqStreamRepFunc) End() {}

type StreamFunc struct {
	Method *Method // function
	reply  IReply
	rw     io.ReadWriteCloser
	buf    *bufio.ReadWriter
}

func NewStreamFunc(m *Method) IMethodFunc {
	// TODO
	return nil
}

func (sf *StreamFunc) FuncMode() FuncMode {
	return sf.Method.mode
}

func (sf *StreamFunc) Call(p *Pack, r IReply) {
	// TODO
}

func (sf *StreamFunc) Next([]msgpack.RawMessage) {
	// TODO
}

func (sf *StreamFunc) End() {}
