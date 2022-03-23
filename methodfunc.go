package zrpc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
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

var (
	pool sync.Pool = sync.Pool{New: func() any {
		return &_methodFunc{}
	}}
)

type methodFunc interface {
	FuncMode() FuncMode
	Call(p *Pack)
	Next(params [][]byte)
	End(err error)
	Release() error
}

type iReply interface {
	Reply(p *Pack) error
	SendError(pack *Pack, e error)
}

func newMethodFunc(method *method, r iReply) (methodFunc, error) {
	base := pool.Get().(*_methodFunc)
	base.init(method, r)

	switch method.mode {
	case ReqRep:
		return newReqRepFunc(base), nil
	case StreamReqRep:
		return newStreamReqRepFunc(base)
	case ReqStreamRep:
		return newReqStreamRepFunc(base), nil
	case Stream:
		return newStreamFunc(base)
	}
	return nil, fmt.Errorf("unknown function type: %+v", method.mode)
}

var _ methodFunc = (*_methodFunc)(nil)

type _methodFunc struct {
	ctx    context.Context
	cancel context.CancelFunc
	Method *method // function
	reply  iReply
	span   trace.Span
}

func (f *_methodFunc) init(m *method, r iReply) {
	f.ctx = nil
	f.cancel = nil
	f.span = nil
	f.Method = m
	f.reply = r
}

func (f *_methodFunc) unmarshalCtx(b []byte) (context.Context, error) {
	if len(b) == 0 {
		ctx, cancel := context.WithCancel(context.Background())
		f.ctx = ctx
		f.cancel = cancel
		return ctx, nil
	}

	ctx := NewContext()
	f.ctx = ctx
	f.cancel = ctx.Cancel
	if err := msgpack.Unmarshal(b, &ctx); err != nil {
		return nil, err
	}

	if tinfo := ctx.Value(TracePayloadKey); !isNil(tinfo) {
		trace := map[string]string{}
		val := reflect.ValueOf(tinfo)
		iter := val.MapRange()
		for iter.Next() {
			k := iter.Key().Interface().(string)
			v := iter.Value().Interface().(string)
			trace[k] = v
		}
		tmpCtx := otel.GetTextMapPropagator().Extract(ctx.Context, propagation.MapCarrier(trace))
		ctx.Context, f.span = otel.GetTracerProvider().Tracer("zrpc-go").Start(tmpCtx, f.Method.methodName)
	}
	return ctx, nil
}

func (f *_methodFunc) assembleParams(params [][]byte) (l int, paramVals []reflect.Value, err error) {
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

func (f *_methodFunc) call(params []reflect.Value) (result [][]byte, err error) {
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

func (f *_methodFunc) setStatus(code codes.Code, desc string) {
	if f.span != nil {
		f.span.SetStatus(code, desc)
	}
}

func (f *_methodFunc) spanEnd() {
	if f.span != nil {
		f.span.End()
	}
}

func (f *_methodFunc) FuncMode() FuncMode { return -1 }
func (f *_methodFunc) Call(p *Pack)       {}
func (f *_methodFunc) Next([][]byte)      {}
func (f *_methodFunc) End(error)          {}
func (f *_methodFunc) Release() error     { pool.Put(f); return nil }

// ReqRepFunc 请求应答类型函数
type reqRepFunc struct {
	*_methodFunc
}

func newReqRepFunc(base *_methodFunc) methodFunc {
	return &reqRepFunc{
		_methodFunc: base,
	}
}

func (f *reqRepFunc) FuncMode() FuncMode {
	return f.Method.mode
}

// Call 将 func 放入 pool 中运行
func (f *reqRepFunc) Call(p *Pack) {
	defer f.Release()
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

func (f *reqRepFunc) Release() error {
	f.cancel()
	f._methodFunc.Release()
	return nil
}

// streamReqRepFunc 流式请求类型函数
type streamReqRepFunc struct {
	*_methodFunc

	req *Pack
	r   io.ReadCloser
	w   io.WriteCloser
	bufw *bufio.Writer

	isClosed bool
	lock     sync.Locker
}

func newStreamReqRepFunc(base *_methodFunc) (methodFunc, error) {
	readCloser, writerCloser := io.Pipe()

	bufw := bufio.NewWriter(writerCloser)//bufio.NewReadWriter(bufio.NewReader(readCloser), bufio.NewWriter(writerCloser))
	return &streamReqRepFunc{
		_methodFunc: base,

		r:   readCloser,
		w:   writerCloser,
		bufw: bufw,

		lock: spinlock.NewSpinLock(),
	}, nil
}

func (srf *streamReqRepFunc) FuncMode() FuncMode {
	return srf.Method.mode
}

func (srf *streamReqRepFunc) Call(p *Pack) {
	defer srf.Release()
	srf.req = p

	// 反序列化参数
	l, params, err := srf.assembleParams(p.Args)
	if err != nil {
		srf.reply.SendError(p, err)
	}
	defer srf.spanEnd()
	// 最后一个请求参数是 Reader
	reader := reflect.ValueOf(srf.r)
	params[l-1] = reader

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

func (srf *streamReqRepFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i < len(raw) {
		n, err := srf.bufw.Write(raw[i:])
		if err != nil {
			srf.reply.SendError(srf.req, err)
			return
		}
		i += n
	}
}

func (srf *streamReqRepFunc) End(error) {
	if err := srf.Release(); err != nil {
		srf.reply.SendError(srf.req, err)
	}
}

func (srf *streamReqRepFunc) Release() error {
	if srf.isClosed {
		return nil
	}

	srf.lock.Lock()
	defer srf.lock.Unlock()
	if srf.isClosed {
		return nil
	}

	srf.isClosed = true
	srf._methodFunc.Release()
	err := srf.bufw.Flush()
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
type reqStreamRepFunc struct {
	*_methodFunc

	req         *Pack
	writeCloser io.WriteCloser

	isClosed bool
	lock     sync.Locker
}

func newReqStreamRepFunc(base *_methodFunc) methodFunc {
	rsf := &reqStreamRepFunc{
		_methodFunc: base,

		lock: spinlock.NewSpinLock(),
	}

	rsf.writeCloser = rsf
	return rsf
}

func (rsf *reqStreamRepFunc) FuncMode() FuncMode {
	return rsf.Method.mode
}

func (rsf *reqStreamRepFunc) Call(p *Pack) {
	defer rsf.Release()
	// 参数最后一个是 writer，只有一个err返回值
	rsf.req = p

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

func (rsf *reqStreamRepFunc) Write(b []byte) (int, error) {
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

func (rsf *reqStreamRepFunc) Close() error {
	if rsf.isClosed {
		return nil
	}

	rsf.lock.Lock()
	defer rsf.lock.Unlock()
	if rsf.isClosed {
		return nil
	}
	rsf.isClosed = true

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
	return rsf.reply.Reply(data)
}

func (rsf *reqStreamRepFunc) Release() error {
	rsf.Close()
	rsf.cancel()
	rsf._methodFunc.Release()
	return nil
}

type readWriteCloser struct {
	io.Reader
	io.Writer
	io.Closer
}

type streamFunc struct {
	*_methodFunc

	req *Pack

	r  io.ReadCloser
	w  io.WriteCloser
	rw io.ReadWriteCloser

	reqStreamIsEnd bool
	isClose        bool
	lock           sync.Locker
}

func newStreamFunc(base *_methodFunc) (methodFunc, error) {
	readCloser, writerCloser := io.Pipe()

	sf := &streamFunc{
		_methodFunc: base,

		r:    readCloser,
		w:    writerCloser,
		lock: spinlock.NewSpinLock(),
	}
	sf.rw = &readWriteCloser{
		Reader: bufio.NewReader(readCloser),
		Writer: sf, // 写入需要及时发送出去
		Closer: sf,
	}
	return sf, nil
}

func (sf *streamFunc) FuncMode() FuncMode {
	return sf.Method.mode
}

func (sf *streamFunc) Call(p *Pack) {
	defer sf.Release()
	// 参数最后一个是 writer，只有一个err返回值
	sf.req = p

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

func (sf *streamFunc) Write(b []byte) (int, error) {
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

func (sf *streamFunc) Close() error {
	if sf.isClose {
		return nil
	}

	sf.lock.Lock()
	defer sf.lock.Unlock()
	if sf.isClose {
		return nil
	}

	sf.isClose = true

	var header = make(Header, len(sf.req.Header))
	for k, v := range sf.req.Header {
		for _, i := range v {
			header.Set(k, i)
		}
	}

	data := &Pack{
		Identity: sf.req.Identity,
		Header:   header,
		Stage:    STREAM_END,
	}
	return sf.reply.Reply(data)
}

func (sf *streamFunc) Next(data [][]byte) {
	raw := data[0]
	var i int
	for i < len(raw) {
		n, err := sf.w.Write(raw[i:])
		if err != nil && err != io.EOF {
			sf.reply.SendError(sf.req, err)
			return
		}
		i += n
	}
}

// End 	请求流结束
func (sf *streamFunc) End(error) {
	sf.lock.Lock()
	defer sf.lock.Unlock()
	if sf.reqStreamIsEnd {
		return
	}

	sf.reqStreamIsEnd = true
	sf.w.Close()
}

func (sf *streamFunc) Release() error {
	defer sf._methodFunc.Release()
	sf.End(nil)
	return sf.Close()
}
