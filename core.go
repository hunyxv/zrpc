package zrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
)

var (
	// errors
	ErrInvalidServer     = errors.New("zrpc: register server err: invalid server")
	ErrNotImplements     = errors.New("zrpc: the type not implements the given interface")
	ErrTooFewReturn      = errors.New("zrpc: too few return values")
	ErrInvalidResultType = errors.New("zrpc: the last return value must be error")
	ErrInvalidParamType  = errors.New("zrpc: the first param must be Context")
	ErrTooFewParam       = errors.New("zrpc: too few parameters")
	ErrSubmitTimeout     = errors.New("zrpc: submit task timed out")

	errType         = reflect.TypeOf(new(error)).Elem()
	ctxType         = reflect.TypeOf(new(context.Context)).Elem()
	readType        = reflect.TypeOf(new(io.Reader)).Elem()
	writeCloserType = reflect.TypeOf(new(io.WriteCloser)).Elem()
)

type FuncMode int

const (
	ReqRep FuncMode = iota
	StreamReqRep
	ReqStreamRep
	Stream
)

type method struct {
	methodName  string
	mode        FuncMode
	srv         reflect.Value
	method      reflect.Value
	paramTypes  []reflect.Type
	resultTypes []reflect.Type
}

type Server struct {
	ServerName string
	methods    map[string]*method // 方法
	instance   reflect.Value      // server 实例
}

// RPCInstance 保存管理 rpc 实例
type RPCInstance struct {
	servers map[string]*Server // server name : server
	rwMutex sync.RWMutex
}

func NewRPCInstance() *RPCInstance {
	return &RPCInstance{
		servers: make(map[string]*Server),
	}
}

// RegisterServer 注册 server
func (rpc *RPCInstance) RegisterServer(name string, server interface{}, conventions interface{}) error {
	rv := reflect.ValueOf(server)
	if rv.IsNil() {
		return ErrInvalidServer
	}

	t := reflect.TypeOf(server)

	if !t.Implements(reflect.TypeOf(conventions).Elem()) {
		return ErrNotImplements
	}

	if name == "" {
		name = t.Name()
	}

	s := &Server{
		ServerName: name,
		methods:    make(map[string]*method),
		instance:   rv,
	}
	for i := 0; i < rv.NumMethod(); i++ {
		var method = new(method)
		t_method := t.Method(i)
		method.srv = s.instance // MethodFunc 的第一个参数
		method.methodName = strings.Join([]string{name, t_method.Name}, "/")
		method.method = rv.Method(i)
		methodType := t_method.Type
		numOfParams := methodType.NumIn()
		if numOfParams <= 1 {
			return ErrTooFewParam
		}
		numOfResult := methodType.NumOut()
		if numOfResult == 0 {
			return ErrTooFewReturn
		}
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
			if method.paramTypes[numOfParams-2].Implements(writeCloserType) {
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
		log.Println("register: ", method.methodName, method.mode)
		s.methods[method.methodName] = method
	}
	rpc.rwMutex.Lock()
	rpc.servers[s.ServerName] = s
	rpc.rwMutex.Unlock()
	return nil
}

// GenerateExecFunc 查找并返回可执行函数
// 	name: /{servername}/methodname
func (rpc *RPCInstance) GenerateExecFunc(name string) (methodFunc, error) {
	rpc.rwMutex.RLock()
	defer rpc.rwMutex.RUnlock()
	nameSlice := strings.SplitN(name, "/", 2) //path.Split(name)
	log.Println(nameSlice)
	serverName, methodName := nameSlice[0], nameSlice[1]
	server, ok := rpc.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("no %s server", serverName)
	}

	method, ok := server.methods[name]
	if !ok {
		return nil, fmt.Errorf("%s server no %s method", serverName, methodName)
	}
	return newMethodFunc(method)
}
