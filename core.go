package zrpc

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"

	"github.com/vmihailenco/msgpack/v5"
)

var (
	// errors
	ErrInvalidServer     = errors.New("zrpc: register server err: invalid server")
	ErrNotImplements     = errors.New("zrpc: the type not implements the given interface")
	ErrInvalidResultType = errors.New("zrpc: the last return value must be error")
	ErrInvalidParamType  = errors.New("zrpc: the first param must be Context")
	ErrTooFewParam       = errors.New("zrpc: too few parameters")

	errType = reflect.TypeOf(new(error)).Elem()
	ctxType = reflect.TypeOf(new(context.Context)).Elem()
)

type MethodFunc struct {
	Method reflect.Value   // function
	Params []reflect.Value // params
}

func (f MethodFunc) Call() (ret []interface{}) {
	for _, item := range f.Method.Call(f.Params) {
		ret = append(ret, item.Interface())
	}
	return
}

type Method struct {
	methodName  string
	method      reflect.Value
	paramTypes  []reflect.Type
	resultTypes []reflect.Type
}

type Server struct {
	ServerName  string
	methods     map[string]Method
	conventions interface{}
	instance    interface{}
}

type RPCInstance struct {
	servers map[string]*Server // server name : server
}

// RegisterServer 注册 server
func (rpc *RPCInstance) RegisterServer(name string, server interface{}, conventions interface{}) error {
	rv := reflect.ValueOf(server)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
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
		ServerName:  name,
		methods:     make(map[string]Method),
		conventions: conventions,
		instance:    server,
	}
	for i := 0; i < rv.NumMethod(); i++ {
		var method Method
		t_method := t.Method(i)
		method.methodName = t_method.Name
		method.method = rv.Method(i)
		methodType := t_method.Type
		numOfParams := methodType.NumIn()
		if numOfParams == 0 {
			return ErrTooFewParam
		}
		numOfResult := methodType.NumOut()
		for j := 0; j < numOfParams; j++ {
			method.paramTypes = append(method.paramTypes, methodType.In(j))
		}
		// 判断第一个参数是否是 Context
		if !method.paramTypes[0].Implements(ctxType) {
			return ErrInvalidParamType
		}
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
func (rpc *RPCInstance) GenerateExecFunc(pctx context.Context, name string, params []msgpack.RawMessage) (*MethodFunc, error) {
	if len(params) == 0 {
		return nil, ErrTooFewParam
	}
	serverName, methodName := path.Split(name)
	server, ok := rpc.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("no %s server", serverName)
	}
	method, ok := server.methods[methodName]
	if !ok {
		return nil, fmt.Errorf("no %s method", methodName)
	}

	// 反序列化参数
	paramsValue := make([]reflect.Value, len(params))
	var ctx *Context
	err := msgpack.Unmarshal(params[0], &ctx)
	if err != nil {
		return nil, fmt.Errorf("zrpc: %s", err.Error())
	}
	paramsValue[0] = reflect.ValueOf(ctx)
	for i := 1; i < len(params); i++ {
		fieldType := method.paramTypes[i]
		fieldValue := reflect.New(fieldType)
		err := msgpack.Unmarshal(params[i], fieldValue.Interface())
		if err != nil {
			return nil, err
		}
		paramsValue[i] = fieldValue
	}

	return &MethodFunc{
		Method: method.method,
		Params: paramsValue,
	}, nil
}
