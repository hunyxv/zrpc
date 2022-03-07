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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type IMethodFunc interface {
	FuncMode() FuncMode
	Call(p *Pack, r IReply)
	Next(params [][]byte)
	End()
	Release() error
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

var _ IMethodFunc = (*MethodFunc)(nil)

type MethodFunc struct {
	ctx    context.Context
	cancel context.CancelFunc
	Method *Method // function
	reply  IReply
	span   trace.Span
}

func (f *MethodFunc) FuncMode() FuncMode     { return -1 }
func (f *MethodFunc) Call(p *Pack, r IReply) {}
func (f *MethodFunc) Next([][]byte)          {}
func (f *MethodFunc) End()                   {}
func (f *MethodFunc) Release() error         { return nil }

func (f *MethodFunc) unmarshalCtx(b []byte) (context.Context, error) {
	if len(b) == 0 {
		return context.Background(), nil
	}

	ctx := NewContext(f.ctx)
	if err := msgpack.Unmarshal(b, &ctx); err != nil {
		return nil, err
	}

	if tinfo := ctx.Value(TracePayloadKey); !isNil(tinfo) {
		if m, ok := tinfo.(map[string]string); ok {
			ctx.Context = otel.GetTextMapPropagator().Extract(ctx.Context, propagation.MapCarrier(m))
			_, f.span = otel.GetTracerProvider().Tracer("zrpc-go").Start(ctx, f.Method.methodName)
			return ctx, nil
		}
	}
	return ctx, nil
}

func (f *MethodFunc) setStatus(code codes.Code, desc string) {
	if f.span != nil {
		f.span.SetStatus(code, desc)
	}
}

func (f *MethodFunc) spanEnd() {
	if f.span != nil {
		f.span.End()
	}
}

var _ IMethodFunc = (*ReqRepFunc)(nil)

// ReqRepFunc 请求应答类型函数
type ReqRepFunc struct {
	*MethodFunc
}

func NewReqRepFunc(m *Method) IMethodFunc {
	ctx, cancel := context.WithCancel(context.Background())
	return &ReqRepFunc{
		MethodFunc: &MethodFunc{
			ctx:    ctx,
			cancel: cancel,
			Method: m,
		},
	}
}

func (f *ReqRepFunc) FuncMode() FuncMode {
	return f.Method.mode
}

// Call 将 func 放入 pool 中运行
func (f *ReqRepFunc) Call(p *Pack, r IReply) {
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
	ctx, err := f.unmarshalCtx(p.Args[0])
	if err != nil {
		log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
		f.reply.SendError(p, err)
		return
	}
	defer f.spanEnd()
	paramsValue[0] = reflect.ValueOf(ctx)
	for i := 1; i < len(f.Method.paramTypes); i++ {
		fieldType := f.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			f.setStatus(codes.Error, "zrpc: Internal Server Error")
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
			f.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue
	}

	var rets [][]byte
	result := f.Method.method.Call(paramsValue)
	for _, item := range result[:len(result)-1] {
		ret, err := msgpack.Marshal(item.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
			f.reply.SendError(p, err)
			return
		}
		rets = append(rets, ret)
	}

	// 最后一个是error类型
	errVal := result[len(result)-1]
	if !errVal.IsNil() {
		err = errVal.Interface().(error)
		f.setStatus(codes.Error, err.Error())
		ret, _ := msgpack.Marshal(err.Error())
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
	f.reply.Reply(resp)
}

func (f *ReqRepFunc) Release() error {
	f.cancel()
	return nil
}

// StreamReqRepFunc 流式请求类型函数
type StreamReqRepFunc struct {
	*MethodFunc

	req *Pack
	r   io.ReadCloser
	w   io.WriteCloser
	buf *bufio.ReadWriter

	isClosed bool
	lock     sync.Locker
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
		MethodFunc: &MethodFunc{
			ctx:    ctx,
			cancel: cancel,
			Method: m,
		},

		r:   readCloser,
		w:   writerCloser,
		buf: buf,

		lock: spinlock.NewSpinLock(),
	}, nil
}

func (srf *StreamReqRepFunc) FuncMode() FuncMode {
	return srf.Method.mode
}

func (srf *StreamReqRepFunc) Call(p *Pack, ireplye IReply) {
	srf.reply = ireplye
	srf.req = p

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
	ctx, err := srf.unmarshalCtx(p.Args[0])
	if err != nil {
		log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
		srf.reply.SendError(p, err)
		return
	}
	defer srf.spanEnd()
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(srf.Method.paramTypes)-1; i++ {
		fieldType := srf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
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
		err = errVal.Interface().(error)
		srf.setStatus(codes.Error, err.Error())
		ret, _ := msgpack.Marshal(err.Error())
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
		if err != nil {
			srf.reply.SendError(srf.req, err)
			return
		}
		i += n
	}
}

func (srf *StreamReqRepFunc) End() {
	if err := srf.Release(); err != nil {
		srf.reply.SendError(srf.req, err)
	}
}

func (srf *StreamReqRepFunc) Release() error {
	if srf.isClosed {
		return nil
	}

	srf.lock.Lock()
	defer srf.lock.Unlock()
	if srf.isClosed {
		return nil
	}

	srf.isClosed = true
	err := srf.buf.Flush()
	if err != nil {
		srf.cancel()
		return err
	}
	srf.cancel()
	return srf.w.Close()
}

type writeCloser struct {
	*bufio.Writer
	io.Closer
}

type ReqStreamRepFunc struct {
	*MethodFunc

	ctx    context.Context
	cancel context.CancelFunc
	id     string
	req    *Pack
	Method *Method // function
	reply  IReply

	writeCloser *writeCloser

	isClosed bool
	lock     sync.Locker
}

func NewReqStreamRepFunc(m *Method) IMethodFunc {
	ctx, cancel := context.WithCancel(context.Background())
	rsf := &ReqStreamRepFunc{
		ctx:    ctx,
		cancel: cancel,
		Method: m,
		lock:   spinlock.NewSpinLock(),
	}
	writer := bufio.NewWriter(rsf)
	rsf.writeCloser = &writeCloser{
		Writer: writer,
		Closer: rsf,
	}
	return rsf
}

func (rsf *ReqStreamRepFunc) FuncMode() FuncMode {
	return rsf.Method.mode
}

func (rsf *ReqStreamRepFunc) Call(p *Pack, r IReply) {
	// 参数最后一个是 writer，只有一个err返回值
	rsf.id = p.Identity
	rsf.req = p
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
		log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
		rsf.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)

	for i := 1; i < len(rsf.Method.paramTypes)-1; i++ {
		fieldType := rsf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
			rsf.reply.SendError(p, err)
			return
		}
		paramsValue[i] = fieldValue.Elem()
	}
	// 最后一个是 WriteCloser
	rwvalue := reflect.ValueOf(rsf.writeCloser)
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
	var header = make(Header, len(rsf.req.Header))
	for k, v := range rsf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}
	data := &Pack{
		Identity: rsf.id,
		Header:   header,
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
	return rsf.Release()
}

func (rsf *ReqStreamRepFunc) Release() error {
	if rsf.isClosed {
		return nil
	}
	rsf.lock.Lock()
	defer rsf.lock.Unlock()
	if rsf.isClosed {
		return nil
	}
	rsf.isClosed = true
	if err := rsf.writeCloser.Flush(); err != nil {
		return err
	}

	var header = make(Header, len(rsf.req.Header))
	for k, v := range rsf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}
	data := &Pack{
		Identity: rsf.id,
		Header:   header,
		Stage:    STREAM_END,
	}
	defer rsf.cancel()
	return rsf.reply.Reply(data)
}

type readWriteCloser struct {
	io.Reader
	io.Writer
	io.Closer
}

type StreamFunc struct {
	*MethodFunc

	ctx    context.Context
	cancel context.CancelFunc
	Method *Method // function
	reply  IReply
	id     string
	req    *Pack

	r    io.ReadCloser
	w    io.WriteCloser
	bufw *bufio.Writer
	rw   io.ReadWriteCloser

	reqStreamIsEnd bool
	isClose        bool
	lock           sync.Locker
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

	sf.rw = &readWriteCloser{
		Reader: bufio.NewReader(readCloser),
		Writer: sf, // 写入需要及时发送出去
		Closer: sf,
	}
	return sf, nil
}

func (sf *StreamFunc) FuncMode() FuncMode {
	return sf.Method.mode
}

func (sf *StreamFunc) Call(p *Pack, r IReply) {
	// 参数最后一个是 writer，只有一个err返回值
	sf.id = p.Identity
	sf.req = p
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
		log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
		sf.reply.SendError(p, err)
		return
	}
	paramsValue[0] = reflect.ValueOf(ctx)
	for i := 1; i < len(sf.Method.paramTypes)-1; i++ {
		fieldType := sf.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(p.Args[i], fieldValue.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
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
	var header = make(Header, len(sf.req.Header))
	for k, v := range sf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}
	data := &Pack{
		Identity: sf.id,
		Header:   header,
		Stage:    STREAM,
		Args:     [][]byte{b},
	}
	err := sf.reply.Reply(data)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (sf *StreamFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i != len(raw) {
		n, err := sf.bufw.Write(raw[i:])
		if err != nil && err != io.EOF {
			sf.reply.SendError(sf.req, err)
			return
		}
		i += n
	}
	sf.bufw.Flush()
}

func (sf *StreamFunc) End() {
	sf.lock.Lock()
	defer sf.lock.Unlock()
	if sf.reqStreamIsEnd {
		return
	}
	sf.reqStreamIsEnd = true
	sf.bufw.Flush()
	sf.cancel()
	sf.w.Close()
}

func (sf *StreamFunc) Close() error {
	if sf.isClose {
		return nil
	}

	var header = make(Header, len(sf.req.Header))
	for k, v := range sf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}
	sf.isClose = true
	data := &Pack{
		Identity: sf.id,
		Header:   header,
		Stage:    STREAM_END,
	}
	return sf.reply.Reply(data)
}

func (sf *StreamFunc) Release() error {
	sf.End()
	defer sf.cancel()
	return sf.Close()
}
