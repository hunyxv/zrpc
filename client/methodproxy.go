package client

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/hunyxv/zrpc"
	pkgerr "github.com/pkg/errors"
)

var (
	ErrTooFewParam       = errors.New("zrpc-cli: too few parameters")
	ErrTooFewReturn      = errors.New("zrpc-cli: too few return values")
	ErrInvalidParamType  = errors.New("zrpc-cli: the first param must be Context")
	ErrInvalidResultType = errors.New("zrpc-cli: the last return value must be error")

	errType         = reflect.TypeOf(new(error)).Elem()
	ctxType         = reflect.TypeOf(new(context.Context)).Elem()
	readType        = reflect.TypeOf(new(io.Reader)).Elem()
	writeCloserType = reflect.TypeOf(new(io.WriteCloser)).Elem()
)

type method struct {
	methodName  string
	mode        zrpc.FuncMode
	paramTypes  []reflect.Type
	resultTypes []reflect.Type
}

type instanceProxy struct {
	InstanceName string      // 实例名称
	instance     interface{} // 实例

	channelManager *channelManager
}

func newInstanceProxy(instanceName string, instance interface{}, chManager *channelManager) *instanceProxy {
	return &instanceProxy{
		InstanceName: instanceName,
		instance:     instance,

		channelManager: chManager,
	}
}

func (proxy *instanceProxy) init() error {
	value := reflect.ValueOf(proxy.instance)
	//_type := reflect.TypeOf(proxy.instance)
	return proxy.replace(value, nil, 0)
}

func (proxy *instanceProxy) replace(v reflect.Value, t reflect.Type,  index int) error {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			err := proxy.replace(v.Field(i), v.Type(), i)
			if err != nil {
				return err
			}
		}
	} else if v.Kind() == reflect.Func {
		numOfResult := v.Type().NumOut()
		if numOfResult == 0 {
			return ErrTooFewReturn
		}

		numOfParams := v.Type().NumIn()
		if numOfParams == 0 {
			return ErrTooFewParam
		}

		methodName := t.Field(index).Name
		methodType := v.Type()

		// 判断第1个参数是否是 Context
		if !methodType.In(0).Implements(ctxType) {
			return ErrInvalidParamType
		}

		var mode zrpc.FuncMode = zrpc.ReqRep
		// 参数大于1
		if numOfParams > 1 {
			// 最后一个参数实现了 io.Reader
			if methodType.In(numOfParams - 1).Implements(readType) {
				mode |= zrpc.StreamReqRep
			}
			// 最后一个参数实现了 io.Writer
			if methodType.In(numOfParams - 1).Implements(writeCloserType) {
				mode |= zrpc.ReqStreamRep
			}
		}

		// 判断最后一个返回值是否是 error
		if !methodType.Out(numOfResult - 1).Implements(errType) {
			return ErrInvalidResultType
		}

		var paramTypes []reflect.Type
		for i := 0; i < numOfParams; i++ {
			paramTypes = append(paramTypes, methodType.In(i))
		}

		var resultTypes []reflect.Type
		for i := 0; i < numOfResult; i++ {
			resultTypes = append(resultTypes, methodType.Out(i))
		}

		v.Set(proxy.MakeFunc(methodType, method{
			methodName:  strings.Join([]string{proxy.InstanceName, methodName}, "/"),
			mode:        mode,
			paramTypes:  paramTypes,
			resultTypes: resultTypes,
		}))
	}
	return nil
}

func (proxy *instanceProxy) MakeFunc(methodType reflect.Type, m method) reflect.Value {
	return reflect.MakeFunc(methodType, func(args []reflect.Value) (results []reflect.Value) {
		ch, err := newMethodChannle(m, proxy.channelManager)
		if err != nil {
			err = pkgerr.WithMessage(err, "[zrpc-cli]: failed to initialize channel")
			for _, t := range m.resultTypes[:len(m.resultTypes)-1] {
				r := reflect.New(t)
				results = append(results, r)
			}
			results = append(results, reflect.ValueOf(err))
			return
		}

		proxy.channelManager.insertNewChannel(m.methodName, ch)
		defer proxy.channelManager.removeChannel(m.methodName, ch.MsgID())
		return ch.Call(args)
	})
}
