package zrpc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"sync"

	"github.com/hunyxv/utils/spinlock"
	"github.com/vmihailenco/msgpack/v5"
)

type IMethodFunc interface {
	FuncMode() FuncMode
	Call(p *Pack, r IReply)
	Next(params [][]byte)
	End()
}

type IReply interface {
	Reply(p *Pack) error
	SendError(pack *Pack, e error)
}

func NewMethodFunc(method *Method) (IMethodFunc, error) {
	switch method.mode {
	case ReqRep:
		return NewReqRepFunc(method), nil
	case StreamReqRep:
		return NewStreamReqRepFunc(method)
	case ReqStreamRep:
		return NewReqStreamRepFunc(method), nil
	case Stream:
		return NewStreamFunc(method)
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
	f.reply = r

	if len(p.Args) != len(f.Method.paramTypes) {
		f.reply.SendError(p, fmt.Errorf("not enough arguments in call to %s", f.Method.methodName))
		return
	}

	defer func() {
		if e := recover(); e != nil {
			log.Printf("[panic]: method name: %s, err: %+v", f.Method.methodName, e)
			f.reply.SendError(p, fmt.Errorf("%+v", e))
		}
	}()

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))

	var ctx = NewContext(context.Background())
	err := msgpack.Unmarshal(p.Args[0], ctx)
	if err != nil {
		log.Println("err: ", err)
		f.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(f.Method.paramTypes); i++ {
		fieldType := f.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			f.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue
	}

	var rets [][]byte
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

func (f *ReqRepFunc) Next([][]byte) {}
func (f *ReqRepFunc) End()          {}

// StreamReqRepFunc 流式请求类型函数
type StreamReqRepFunc struct {
	ctx    context.Context
	cancel context.CancelFunc
	id     string
	header Header
	Method *Method // function
	reply  IReply
	buf    *bufio.ReadWriter
	r      io.ReadCloser
	w      io.WriteCloser
}

func NewStreamReqRepFunc(m *Method) (IMethodFunc, error) {
	// 暂时不知道好用不好用 os.Pipe , 替代品为 rwchannel
	readCloser, writerCloser, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	buf := bufio.NewReadWriter(bufio.NewReader(readCloser), bufio.NewWriter(writerCloser))
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamReqRepFunc{
		ctx:    ctx,
		cancel: cancel,
		r:      readCloser,
		w:      writerCloser,
		buf:    buf,
		Method: m,
	}, nil
}

func (srf *StreamReqRepFunc) FuncMode() FuncMode {
	return srf.Method.mode
}

func (srf *StreamReqRepFunc) Call(p *Pack, ireplye IReply) {
	srf.id = p.Identity
	srf.reply = ireplye
	srf.header = p.Header

	if len(p.Args) != len(srf.Method.paramTypes) {
		srf.reply.SendError(p, fmt.Errorf("not enough arguments in call to %s", srf.Method.methodName))
		return
	}

	defer func() {
		if e := recover(); e != nil {
			log.Printf("[panic]: method name: %s, err: %+v", srf.Method.methodName, e)
			srf.reply.SendError(p, fmt.Errorf("%+v", e))
		}
	}()

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// 第一个参数是 ctx
	var ctx = NewContext(srf.ctx)
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		log.Println("err: ", err)
		srf.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(srf.Method.paramTypes)-1; i++ {
		fieldType := srf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Println("err: ", err)
			srf.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue
	}
	// 最后一个是 ReadWriter
	rwvalue := reflect.ValueOf(srf.buf)
	paramsValue[len(srf.Method.paramTypes)-1] = rwvalue

	var rets [][]byte
	// 调用函数
	result := srf.Method.method.Call(paramsValue)
	srf.r.Close()
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
		Header:   p.Header,
		Stage:    REPLY,
		Args:     rets,
	}
	resp.SetMethodName(p.MethodName())
	srf.reply.Reply(resp)
}

func (srf *StreamReqRepFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i != len(raw) {
		n, err := srf.buf.Write(raw[i:])
		if err != nil && err != io.EOF {
			srf.reply.SendError(&Pack{Identity: srf.id, Header: srf.header}, err)
			return
		}
		i += n
	}
}

func (srf *StreamReqRepFunc) End() {
	srf.buf.Flush()
	srf.cancel()
	srf.w.Close()
}

type ReqStreamRepFunc struct {
	ctx    context.Context
	cancel context.CancelFunc
	id     string
	header Header
	Method *Method // function
	reply  IReply
	writer *bufio.Writer
}

func NewReqStreamRepFunc(m *Method) IMethodFunc {
	ctx, cancel := context.WithCancel(context.Background())
	rsf := &ReqStreamRepFunc{
		ctx:    ctx,
		cancel: cancel,
		Method: m,
	}
	writer := bufio.NewWriter(rsf)
	rsf.writer = writer
	return rsf
}

func (rsf *ReqStreamRepFunc) FuncMode() FuncMode {
	return rsf.Method.mode
}

func (rsf *ReqStreamRepFunc) Call(p *Pack, r IReply) {
	// 参数最后一个是 writer，只有一个err返回值
	rsf.id = p.Identity
	rsf.header = p.Header
	rsf.reply = r
	if len(p.Args) != len(rsf.Method.paramTypes) {
		rsf.reply.SendError(p, fmt.Errorf("not enough arguments in call to %s", rsf.Method.methodName))
		return
	}
	defer func() {
		if e := recover(); e != nil {
			log.Printf("[panic]: method name: %s, err: %+v", rsf.Method.methodName, e)
			rsf.reply.SendError(p, fmt.Errorf("%+v", e))
		}
	}()

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// 第一个参数是 ctx
	var ctx = NewContext(rsf.ctx)
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		log.Println("err: ", err)
		rsf.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(rsf.Method.paramTypes)-1; i++ {
		fieldType := rsf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Println("err: ", err)
			rsf.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue.Elem()
	}
	// 最后一个是 Writer
	rwvalue := reflect.ValueOf(rsf.writer)
	paramsValue[len(rsf.Method.paramTypes)-1] = rwvalue

	var rets [][]byte
	// 调用函数
	result := rsf.Method.method.Call(paramsValue)
	if err := rsf.Close(); err != nil {
		rsf.reply.SendError(p, err)
		return
	}

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
		Header:   p.Header,
		Stage:    REPLY,
		Args:     rets,
	}
	resp.SetMethodName(p.MethodName())
	rsf.reply.Reply(resp)
}

func (rsf *ReqStreamRepFunc) Write(b []byte) (int, error) {
	data := &Pack{
		Identity: rsf.id,
		Header:   rsf.header,
		Stage:    STREAM,
		Args:     [][]byte{b},
	}
	err := rsf.reply.Reply(data)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (rsf *ReqStreamRepFunc) Close() error {
	rsf.writer.Flush()
	data := &Pack{
		Identity: rsf.id,
		Header:   rsf.header,
		Stage:    STREAM_END,
	}
	return rsf.reply.Reply(data)
}

func (rsf *ReqStreamRepFunc) Next([][]byte) {}
func (rsf *ReqStreamRepFunc) End()          {}

type readWriter struct {
	io.Reader
	io.Writer
}

type StreamFunc struct {
	ctx     context.Context
	cancel  context.CancelFunc
	id      string
	header  Header
	r       io.ReadCloser
	w       io.WriteCloser
	bufw    *bufio.Writer
	rw      *readWriter
	Method  *Method // function
	reply   IReply
	isClose bool
	lock    sync.Locker
}

func NewStreamFunc(m *Method) (IMethodFunc, error) {
	readCloser, writerCloser, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	sf := &StreamFunc{
		ctx:    ctx,
		cancel: cancel,
		Method: m,
		r:      readCloser,
		w:      writerCloser,
		bufw:   bufio.NewWriter(writerCloser),
		lock:   spinlock.NewSpinLock(),
	}

	sf.rw = &readWriter{
		Reader: bufio.NewReader(readCloser),
		Writer: sf, // 写入需要及时发送出去
	}
	return sf, nil
}

func (sf *StreamFunc) FuncMode() FuncMode {
	return sf.Method.mode
}

func (sf *StreamFunc) Call(p *Pack, r IReply) {
	// 参数最后一个是 writer，只有一个err返回值
	sf.id = p.Identity
	sf.header = p.Header
	sf.reply = r
	if len(p.Args) != len(sf.Method.paramTypes) {
		sf.reply.SendError(p, fmt.Errorf("not enough arguments in call to %s", sf.Method.methodName))
		return
	}
	defer func() {
		if e := recover(); e != nil {
			log.Printf("[panic]: method name: %s, err: %+v", sf.Method.methodName, e)
			sf.reply.SendError(p, fmt.Errorf("%+v", e))
		}
	}()

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(p.Args))
	// 第一个参数是 ctx
	var ctx = NewContext(sf.ctx)
	err := msgpack.Unmarshal(p.Args[0], &ctx)
	if err != nil {
		log.Println("err: ", err)
		sf.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(sf.Method.paramTypes)-1; i++ {
		fieldType := sf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Println("err: ", err)
			sf.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue.Elem()
	}

	// 最后一个参数是 ReadWriter
	rwvalue := reflect.ValueOf(sf.rw)
	paramsValue[len(sf.Method.paramTypes)-1] = rwvalue

	var rets [][]byte
	// 调用函数
	result := sf.Method.method.Call(paramsValue)
	sf.Close()
	//sf.r.Close()
	//sf.End()
	if err := sf.Close(); err != nil {
		sf.reply.SendError(p, err)
		return
	}

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
		Header:   p.Header,
		Stage:    REPLY,
		Args:     rets,
	}
	resp.SetMethodName(p.MethodName())
	sf.reply.Reply(resp)
}

func (sf *StreamFunc) Write(b []byte) (int, error) {
	data := &Pack{
		Identity: sf.id,
		Header:   sf.header,
		Stage:    STREAM,
		Args:     [][]byte{b},
	}
	err := sf.reply.Reply(data)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (sf *StreamFunc) Close() error {
	data := &Pack{
		Identity: sf.id,
		Header:   sf.header,
		Stage:    STREAM_END,
	}
	return sf.reply.Reply(data)
}

func (sf *StreamFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i != len(raw) {
		n, err := sf.bufw.Write(raw[i:])
		if err != nil && err != io.EOF {
			sf.reply.SendError(&Pack{Identity: sf.id, Header: sf.header}, err)
			return
		}
		i += n
	}
	sf.bufw.Flush()
}

func (sf *StreamFunc) End() {
	sf.lock.Lock()
	defer sf.lock.Unlock()
	if sf.isClose {
		return
	}
	sf.isClose = true
	sf.bufw.Flush()
	sf.cancel()
	sf.w.Close()
}
