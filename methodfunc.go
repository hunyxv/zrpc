package zrpc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"sync"

	"github.com/hunyxv/utils/spinlock"
	"github.com/pkg/errors"
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
			ctx.Context, f.span = otel.GetTracerProvider().Tracer("zrpc-go").Start(ctx.Context, f.Method.methodName)
			return ctx, nil
		}
	}
	return ctx, nil
}

func (f *MethodFunc) assembleParams(params [][]byte) (l int, paramVals []reflect.Value, err error) {
	l = len(f.Method.paramTypes)
	if len(params) != l {
		err = fmt.Errorf("not enough arguments in call to %s", f.Method.methodName)
		return
	}

	// 反序列化参数
	paramVals = make([]reflect.Value, len(params))
	// 第一个参数是 ctx
	ctx, err := f.unmarshalCtx(params[0])
	if err != nil {
		log.Printf("StreamReqRepFunc err: arguments unmarshal fail: %v", err)
		return
	}
	paramVals[0] = reflect.ValueOf(ctx)

	for i := 1; i < l-1; i++ {
		fieldType := f.Method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err = msgpack.Unmarshal(params[i], fieldValue.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
			return
		}
		paramVals[i] = fieldValue.Elem()
	}

	if f.Method.mode == ReqRep && l > 1 {
		fieldType := f.Method.paramTypes[l-1]
		fieldValue := reflect.New(fieldType)
		err = msgpack.Unmarshal(params[l-1], fieldValue.Interface())
		if err != nil {
			log.Printf("ReqRep err: arguments unmarshal fail: %v", err)
			return
		}
		paramVals[l-1] = fieldValue.Elem()
	}
	return
}

func (f *MethodFunc) call(params []reflect.Value) (result [][]byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = errors.WithStack(fmt.Errorf("%+v", e))
			log.Printf("[panic]: method name: %s, err: %+v", f.Method.methodName, err)
			return
		}
	}()
	rets := f.Method.method.Call(params)
	for _, item := range rets[:len(rets)-1] {
		ret, err := msgpack.Marshal(item.Interface())
		if err != nil {
			log.Printf("ReqRepFunc err: arguments unmarshal fail: %v", err)
			return nil, err
		}
		result = append(result, ret)
	}

	// 最后一个返回值是error类型
	errVal := rets[len(rets)-1]
	if !errVal.IsNil() {
		e := errVal.Interface().(error)
		f.setStatus(codes.Error, e.Error()) // 链路追踪标记 ERROR
		ret, _ := msgpack.Marshal(e.Error())
		result = append(result, ret)
		return
	}
	ret, _ := msgpack.Marshal(nil)
	result = append(result, ret)
	return
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

func (f *MethodFunc) FuncMode() FuncMode     { return -1 }
func (f *MethodFunc) Call(p *Pack, r IReply) {}
func (f *MethodFunc) Next([][]byte)          {}
func (f *MethodFunc) End()                   {}
func (f *MethodFunc) Release() error         { return nil }

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

	// 反序列化参数
	_, params, err := f.assembleParams(p.Args)
	if err != nil {
		f.reply.SendError(p, err)
	}
	defer f.spanEnd()

	results, err := f.call(params)
	if err != nil {
		f.reply.SendError(p, err)
		return
	}

	resp := &Pack{
		Identity: p.Identity,
		Header:   p.Header,
		Stage:    REPLY,
		Args:     results,
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

	// 反序列化参数
	l, params, err := srf.assembleParams(p.Args)
	if err != nil {
		srf.reply.SendError(p, err)
	}
	defer srf.spanEnd()
	// 最后一个请求参数是 ReadWriter
	rwvalue := reflect.ValueOf(srf.buf)
	params[l-1] = rwvalue

	results, err := srf.call(params)
	if err != nil {
		srf.reply.SendError(p, err)
		return
	}

	resp := &Pack{
		Identity: p.Identity,
		Header:   p.Header,
		Stage:    REPLY,
		Args:     results,
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

// ReqStreamRepFunc 流式响应类型函数
type ReqStreamRepFunc struct {
	*MethodFunc

	req         *Pack
	writeCloser *writeCloser

	isClosed bool
	lock     sync.Locker
}

func NewReqStreamRepFunc(m *Method) IMethodFunc {
	ctx, cancel := context.WithCancel(context.Background())
	rsf := &ReqStreamRepFunc{
		MethodFunc: &MethodFunc{
			ctx:    ctx,
			cancel: cancel,
			Method: m,
		},

		lock: spinlock.NewSpinLock(),
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
	rsf.req = p
	rsf.reply = r

	// 反序列化参数
	l, params, err := rsf.assembleParams(p.Args)
	if err != nil {
		rsf.reply.SendError(p, err)
	}
	defer rsf.spanEnd()
	// 最后一个是 WriteCloser
	rwvalue := reflect.ValueOf(rsf.writeCloser)
	params[l-1] = rwvalue

	results, err := rsf.call(params)
	if err != nil {
		rsf.reply.SendError(p, err)
		return
	}

	resp := &Pack{
		Identity: p.Identity,
		Header:   p.Header,
		Stage:    REPLY,
		Args:     results,
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
		Identity: rsf.req.Identity,
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
		Identity: rsf.req.Identity,
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
	http.Flusher
}

type StreamFunc struct {
	*MethodFunc

	req *Pack

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
		MethodFunc: &MethodFunc{
			ctx:    ctx,
			cancel: cancel,
			Method: m,
		},

		r:    readCloser,
		w:    writerCloser,
		lock: spinlock.NewSpinLock(),
	}
	sf.bufw = bufio.NewWriter(sf)
	sf.rw = &readWriteCloser{
		Reader:  bufio.NewReader(readCloser),
		Writer:  sf.bufw, // 写入需要及时发送出去
		Closer:  sf,
		Flusher: sf,
	}
	return sf, nil
}

func (sf *StreamFunc) FuncMode() FuncMode {
	return sf.Method.mode
}

func (sf *StreamFunc) Call(p *Pack, r IReply) {
	// 参数最后一个是 writer，只有一个err返回值
	sf.req = p
	sf.reply = r

	// 反序列化参数
	l, params, err := sf.assembleParams(p.Args)
	if err != nil {
		sf.reply.SendError(p, err)
	}
	defer sf.spanEnd()

	// 最后一个参数是 ReadWriter
	rwvalue := reflect.ValueOf(sf.rw)
	params[l-1] = rwvalue

	results, err := sf.call(params)
	if err != nil {
		sf.reply.SendError(p, err)
		return
	}

	resp := &Pack{
		Identity: p.Identity,
		Header:   p.Header,
		Stage:    REPLY,
		Args:     results,
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
		Identity: sf.req.Identity,
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

func (sf *StreamFunc) Flush() {
	sf.bufw.Flush()
}

func (sf *StreamFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i != len(raw) {
		n, err := sf.w.Write(raw[i:])
		if err != nil && err != io.EOF {
			sf.reply.SendError(sf.req, err)
			return
		}
		i += n
	}
}

func (sf *StreamFunc) End() {
	sf.lock.Lock()
	defer sf.lock.Unlock()
	if sf.reqStreamIsEnd {
		return
	}
	sf.reqStreamIsEnd = true
	//sf.bufw.Flush()
	sf.cancel()
	sf.w.Close()
}

func (sf *StreamFunc) Close() error {
	if sf.isClose {
		return nil
	}

	sf.bufw.Flush()
	var header = make(Header, len(sf.req.Header))
	for k, v := range sf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}
	sf.isClose = true
	data := &Pack{
		Identity: sf.req.Identity,
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
