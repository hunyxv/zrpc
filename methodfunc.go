package zrpc

import (
	"context"
	"fmt"
	"io"
	"log"
	"reflect"
	"sync"
	"sync/atomic"

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
	Next(p *Pack)
	End(err error)
	Release() error
}

type iReply interface {
	Reply(to string, p *Pack) error
	SendError(to string, e error)
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
func (f *_methodFunc) Call(*Pack)         {}
func (f *_methodFunc) Next(*Pack)         {}
func (f *_methodFunc) End(error)          {}
func (f *_methodFunc) Release() error     { pool.Put(f); return nil }

// ReqRepFunc 请求应答类型函数
type reqRepFunc struct {
	*_methodFunc

	finished int32
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
		f.reply.SendError(p.Identity, err)
	}
	defer f.spanEnd()

	results, err := f.call(params)
	if err != nil {
		f.reply.SendError(p.Identity, err)
		return
	}

	resp := &Pack{
		Stage: REPLY,
		Args:  results,
	}
	resp.SetMethodName(p.MethodName())
	resp.Set(MESSAGEID, p.Get(MESSAGEID))
	f.reply.Reply(p.Identity, resp)
}

func (f *reqRepFunc) Release() error {
	if ok := atomic.CompareAndSwapInt32(&(f.finished), 0, 1); ok {
		f.cancel()
		f._methodFunc.Release()
	}
	return nil
}

// streamReqRepFunc 流式请求类型函数
type streamReqRepFunc struct {
	*_methodFunc

	req      *Pack
	q        *HeapQueue
	finished int32
}

func newStreamReqRepFunc(base *_methodFunc) (methodFunc, error) {
	return &streamReqRepFunc{
		_methodFunc: base,

		q: NewHeapQueue(),
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
		srf.reply.SendError(p.Identity, err)
	}
	defer srf.spanEnd()
	// 最后一个请求参数是 Reader
	reader := reflect.ValueOf(srf.q)
	params[l-1] = reader

	results, err := srf.call(params)
	if err != nil {
		srf.reply.SendError(p.Identity, err)
		return
	}

	resp := &Pack{
		Stage: REPLY,
		Args:  results,
	}
	resp.SetMethodName(p.MethodName())
	resp.Set(MESSAGEID, p.Get(MESSAGEID))
	srf.reply.Reply(p.Identity, resp)
}

func (srf *streamReqRepFunc) Next(pack *Pack) {
	srf.q.Insert(pack)
}

func (srf *streamReqRepFunc) End(error) {
	if err := srf.Release(); err != nil {
		srf.reply.SendError(srf.req.Identity, err)
	}
}

func (srf *streamReqRepFunc) Release() error {
	if ok := atomic.CompareAndSwapInt32(&(srf.finished), 0, 1); ok {
		srf._methodFunc.Release()
		srf.q.Release()
		srf.cancel()
	}
	return nil
}

// ReqStreamRepFunc 流式响应类型函数
type reqStreamRepFunc struct {
	*_methodFunc

	req         *Pack
	writeCloser io.WriteCloser
	finished    int32
	seq         uint64
}

func newReqStreamRepFunc(base *_methodFunc) methodFunc {
	rsf := &reqStreamRepFunc{
		_methodFunc: base,
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
		rsf.reply.SendError(p.Identity, err)
	}
	defer rsf.spanEnd()
	// 最后一个是 WriteCloser
	rwvalue := reflect.ValueOf(rsf.writeCloser)
	params[l-1] = rwvalue

	results, err := rsf.call(params)
	if err != nil {
		rsf.reply.SendError(p.Identity, err)
		return
	}

	if err := rsf.writeCloser.Close(); err != nil {
		rsf.reply.SendError(p.Identity, err)
		return
	}

	rsf.seq++
	resp := &Pack{
		Stage:      REPLY,
		SequenceID: rsf.seq,
		Args:       results,
	}
	resp.SetMethodName(p.MethodName())
	resp.Set(MESSAGEID, p.Get(MESSAGEID))
	rsf.reply.Reply(p.Identity, resp)
}

func (rsf *reqStreamRepFunc) Write(b []byte) (int, error) {
	rsf.seq++
	data := &Pack{
		Stage:      STREAM,
		SequenceID: rsf.seq,
		Args:       [][]byte{b},
	}
	data.SetMethodName(rsf.req.MethodName())
	data.Set(MESSAGEID, rsf.req.Get(MESSAGEID))
	err := rsf.reply.Reply(rsf.req.Identity, data)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (rsf *reqStreamRepFunc) Close() error {
	rsf.seq++
	data := &Pack{
		Stage:      STREAM_END,
		SequenceID: rsf.seq,
		Args:       [][]byte{{8}},
	}
	data.SetMethodName(rsf.req.MethodName())
	data.Set(MESSAGEID, rsf.req.Get(MESSAGEID))
	return rsf.reply.Reply(rsf.req.Identity, data)
}

func (rsf *reqStreamRepFunc) Release() error {
	if ok := atomic.CompareAndSwapInt32(&(rsf.finished), 0, 1); ok {
		defer rsf._methodFunc.Release()
		rsf.cancel()
	}
	return nil
}

type readWriteCloser struct {
	io.Reader
	io.Writer
	io.Closer
}

type streamFunc struct {
	*_methodFunc

	req         *Pack
	queue       *HeapQueue
	rwCloser    io.ReadWriter
	reqfinished int32
	repfinished int32
	closed      int32
	seq         uint64
}

func newStreamFunc(base *_methodFunc) (methodFunc, error) {
	sf := &streamFunc{
		_methodFunc: base,

		queue: NewHeapQueue(),
	}

	sf.rwCloser = &readWriteCloser{
		Reader: sf.queue,
		Writer: sf,
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
		sf.reply.SendError(p.Identity, err)
	}
	defer sf.spanEnd()

	// 最后一个参数是 ReadWriter
	rwvalue := reflect.ValueOf(sf.rwCloser)
	params[l-1] = rwvalue

	results, err := sf.call(params)
	if err != nil {
		sf.reply.SendError(p.Identity, err)
		return
	}

	sf.seq++
	resp := &Pack{
		Identity:   p.Identity,
		Header:     p.Header,
		Stage:      REPLY,
		SequenceID: sf.seq,
		Args:       results,
	}
	resp.SetMethodName(p.MethodName())
	sf.reply.Reply(p.Identity, resp)
}

func (sf *streamFunc) Write(b []byte) (int, error) {
	sf.seq++
	data := &Pack{
		Stage:      STREAM,
		SequenceID: sf.seq,
		Args:       [][]byte{b},
	}
	data.SetMethodName(sf.req.MethodName())
	data.Set(MESSAGEID, sf.req.Get(MESSAGEID))
	err := sf.reply.Reply(sf.req.Identity, data)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (sf *streamFunc) Close() error {
	if ok := atomic.CompareAndSwapInt32(&sf.closed, 0, 1); !ok {
		return nil
	}

	sf.seq++
	data := &Pack{
		SequenceID: sf.seq,
		Stage:      STREAM_END,
	}
	data.SetMethodName(sf.req.MethodName())
	data.Set(MESSAGEID, sf.req.Get(MESSAGEID))
	return sf.reply.Reply(sf.req.Identity, data)
}

func (sf *streamFunc) Next(pack *Pack) {
	sf.queue.Insert(pack)
}

// End 	请求流结束
func (sf *streamFunc) End(error) {
	if ok := atomic.CompareAndSwapInt32(&(sf.repfinished), 0, 1); ok {
		sf.queue.Release()
	}
}

func (sf *streamFunc) Release() error {
	if ok := atomic.CompareAndSwapInt32(&sf.reqfinished, 0, 1); ok {
		sf.End(nil)
		defer sf._methodFunc.Release()
		return sf.Close()
	}
	return nil
}
