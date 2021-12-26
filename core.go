package zrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
)

var (
	// errors
	ErrInvalidServer     = errors.New("zrpc: register server err: invalid server")
	ErrNotImplements     = errors.New("zrpc: the type not implements the given interface")
	ErrInvalidResultType = errors.New("zrpc: the last return value must be error")
	ErrInvalidParamType  = errors.New("zrpc: the first param must be Context")
	ErrTooFewParam       = errors.New("zrpc: too few parameters")

	errType   = reflect.TypeOf(new(error)).Elem()
	ctxType   = reflect.TypeOf(new(context.Context)).Elem()
	readType  = reflect.TypeOf(new(io.Reader)).Elem()
	writeType = reflect.TypeOf(new(io.Writer)).Elem()
	rwCloser  = reflect.TypeOf(new(io.ReadWriteCloser)).Elem()
)

type FuncMode int

const (
	ReqRep FuncMode = iota
	StreamReqRep
	ReqStreamRep
	Stream
)

type Method struct {
	methodName  string
	mode        FuncMode
	srv         reflect.Value
	method      reflect.Value
	paramTypes  []reflect.Type
	resultTypes []reflect.Type
}

type Server struct {
	ServerName string
	methods    map[string]*Method // 方法
	instance   reflect.Value      // server 实例
}

type RPCInstance struct {
	servers map[string]*Server // server name : server
}

func newRPCInstance ()*RPCInstance {
	return &RPCInstance{
		servers: make(map[string]*Server),
	}
}

// RegisterServer 注册 server
func (rpc *RPCInstance) RegisterServer(name string, server interface{}, conventions interface{}) error {
	rv := reflect.ValueOf(server)
	// if rv.Kind() != reflect.Ptr {}
	if rv.IsNil() {
		return ErrInvalidServer
	}

	t := reflect.TypeOf(server)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if !t.Implements(reflect.TypeOf(conventions).Elem()) {
		return ErrNotImplements
	}

	if name == "" {
		name = t.Name()
	}

	s := &Server{
		ServerName: name,
		methods:    make(map[string]*Method),
		// conventions: conventions,
		instance: rv,
	}
	for i := 0; i < rv.NumMethod(); i++ {
		var method = new(Method)
		t_method := t.Method(i)
		method.srv = s.instance // MethodFunc 的第一个参数
		method.methodName = t_method.Name
		method.method = rv.Method(i)
		methodType := t_method.Type
		numOfParams := methodType.NumIn()
		if numOfParams <= 1 {
			return ErrTooFewParam
		}
		numOfResult := methodType.NumOut()
		// 从第一个开始，跳过第0个参数
		for j := 1; j < numOfParams; j++ {
			method.paramTypes = append(method.paramTypes, methodType.In(j))
		}
		// 判断第1个参数是否是 Context
		if !method.paramTypes[0].Implements(ctxType) {
			return ErrInvalidParamType
		}
		var mode FuncMode = ReqRep

		// 参数大于1
		if numOfParams > 1 {
			// 最后一个参数实现了 io.Reader
			if method.paramTypes[numOfParams-2].Implements(readType) {
				mode |= StreamReqRep
			}
			// 最后一个参数实现了 io.Writer
			if method.paramTypes[numOfParams-2].Implements(writeType) {
				mode |= ReqStreamRep
			}
		}
		method.mode = mode
		for j := 0; j < numOfResult; j++ {
			method.resultTypes = append(method.resultTypes, methodType.Out(j))
		}
		// 判断最后一个返回值是否是 error
		if !method.resultTypes[numOfResult-1].Implements(errType) {
			return ErrInvalidResultType
		}

		s.methods[method.methodName] = method
	}
	rpc.servers[s.ServerName] = s
	return nil
}

// GenerateExecFunc 查找并返回可执行函数
// 	name: /{servername}/methodname
func (rpc *RPCInstance) GenerateExecFunc(pctx context.Context, name string) (IMethodFunc, error) {
	serverName, methodName := path.Split(name)
	server, ok := rpc.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("no %s server", serverName)
	}
	method, ok := server.methods[methodName]
	if !ok {
		return nil, fmt.Errorf("no %s method", methodName)
	}

	return NewMethodFunc(method)
}
