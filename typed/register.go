package typed

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/server"
)

var (
	contextType      = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType        = reflect.TypeOf((*error)(nil)).Elem()
	typedPackagePath = reflect.TypeOf(ServerStream[struct{}, struct{}]{}).PkgPath()
)

// Register 使用服务结构体的具体类型名作为服务名进行反射注册。
func Register(srv *server.Server, service any) error {
	name, err := defaultServiceName(service)
	if err != nil {
		return err
	}
	return RegisterService(srv, name, service)
}

// RegisterService 使用指定服务名反射注册服务对象中符合约定签名的方法。
//
// 支持的服务端方法签名：
//   - func(context.Context, *Req) (*Resp, error)
//   - func(context.Context, *ServerStream[Req, Resp]) error
//   - func(context.Context, *Req, *ServerSender[Resp]) error
//   - func(context.Context, *BidiServerStream[Req, Resp]) error
func RegisterService(srv *server.Server, serviceName string, service any) error {
	if srv == nil {
		return errors.New("zrpc/typed: server is nil")
	}
	if serviceName == "" {
		return errors.New("zrpc/typed: service name is required")
	}
	value := reflect.ValueOf(service)
	if !value.IsValid() {
		return errors.New("zrpc/typed: service is nil")
	}
	if value.Kind() == reflect.Ptr && value.IsNil() {
		return errors.New("zrpc/typed: service is nil")
	}

	typ := value.Type()
	registered := 0
	for i := 0; i < typ.NumMethod(); i++ {
		methodInfo := typ.Method(i)
		method := value.Method(i)
		methodType := method.Type()
		switch {
		case isUnaryServiceMethod(methodType):
			reqType := methodType.In(1)
			srv.HandleUnary(serviceName+"."+methodInfo.Name, reflectedUnaryHandler(method, reqType))
			registered++
		case isClientStreamServiceMethod(methodType):
			streamType := methodType.In(1)
			srv.HandleStream(serviceName+"."+methodInfo.Name, reflectedClientStreamHandler(method, streamType))
			registered++
		case isServerStreamServiceMethod(methodType):
			reqType := methodType.In(1)
			streamType := methodType.In(2)
			srv.HandleStream(serviceName+"."+methodInfo.Name, reflectedServerStreamHandler(method, reqType, streamType))
			registered++
		case isBidiStreamServiceMethod(methodType):
			streamType := methodType.In(1)
			srv.HandleStream(serviceName+"."+methodInfo.Name, reflectedBidiStreamHandler(method, streamType))
			registered++
		}
	}
	if registered == 0 {
		return fmt.Errorf("zrpc/typed: service %q has no supported methods", serviceName)
	}
	return nil
}

func defaultServiceName(service any) (string, error) {
	value := reflect.ValueOf(service)
	if !value.IsValid() {
		return "", errors.New("zrpc/typed: service is nil")
	}
	if value.Kind() == reflect.Ptr && value.IsNil() {
		return "", errors.New("zrpc/typed: service is nil")
	}
	typ := value.Type()
	for typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Name() == "" {
		return "", errors.New("zrpc/typed: service concrete type name is required")
	}
	return typ.Name(), nil
}

func isUnaryServiceMethod(methodType reflect.Type) bool {
	return methodType.NumIn() == 2 &&
		methodType.In(0) == contextType &&
		methodType.In(1).Kind() == reflect.Ptr &&
		methodType.NumOut() == 2 &&
		methodType.Out(0).Kind() == reflect.Ptr &&
		methodType.Out(1).Implements(errorType)
}

func isClientStreamServiceMethod(methodType reflect.Type) bool {
	return methodType.NumIn() == 2 &&
		methodType.In(0) == contextType &&
		isTypedStream(methodType.In(1), "ServerStream") &&
		methodType.NumOut() == 1 &&
		methodType.Out(0).Implements(errorType)
}

func isServerStreamServiceMethod(methodType reflect.Type) bool {
	return methodType.NumIn() == 3 &&
		methodType.In(0) == contextType &&
		methodType.In(1).Kind() == reflect.Ptr &&
		isTypedStream(methodType.In(2), "ServerSender") &&
		methodType.NumOut() == 1 &&
		methodType.Out(0).Implements(errorType)
}

func isBidiStreamServiceMethod(methodType reflect.Type) bool {
	return methodType.NumIn() == 2 &&
		methodType.In(0) == contextType &&
		isTypedStream(methodType.In(1), "BidiServerStream") &&
		methodType.NumOut() == 1 &&
		methodType.Out(0).Implements(errorType)
}

func isTypedStream(typ reflect.Type, name string) bool {
	if typ.Kind() != reflect.Ptr {
		return false
	}
	elem := typ.Elem()
	return elem.PkgPath() == typedPackagePath &&
		(elem.Name() == name || strings.HasPrefix(elem.Name(), name+"["))
}

func reflectedUnaryHandler(method reflect.Value, reqType reflect.Type) zrpc.UnaryHandler {
	return zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		in := reflect.New(reqType.Elem())
		if err := req.Decode(in.Interface()); err != nil {
			return nil, err
		}
		out := method.Call([]reflect.Value{reflect.ValueOf(ctx), in})
		if !out[1].IsNil() {
			return nil, out[1].Interface().(error)
		}
		if out[0].IsNil() {
			return zrpc.NewResponse(nil, req.Codec)
		}
		return zrpc.NewResponse(out[0].Interface(), req.Codec)
	})
}

func reflectedClientStreamHandler(method reflect.Value, streamType reflect.Type) zrpc.StreamHandler {
	return zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		out := method.Call([]reflect.Value{reflect.ValueOf(ctx), newTypedStream(streamType, stream)})
		return errorFromValue(out[0])
	})
}

func reflectedServerStreamHandler(method reflect.Value, reqType reflect.Type, streamType reflect.Type) zrpc.StreamHandler {
	return zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		in := reflect.New(reqType.Elem())
		if err := stream.Recv(ctx, in.Interface()); err != nil {
			return err
		}
		out := method.Call([]reflect.Value{reflect.ValueOf(ctx), in, newTypedStream(streamType, stream)})
		return errorFromValue(out[0])
	})
}

func reflectedBidiStreamHandler(method reflect.Value, streamType reflect.Type) zrpc.StreamHandler {
	return zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
		out := method.Call([]reflect.Value{reflect.ValueOf(ctx), newTypedStream(streamType, stream)})
		return errorFromValue(out[0])
	})
}

func newTypedStream(typ reflect.Type, stream zrpc.Stream) reflect.Value {
	value := reflect.New(typ.Elem())
	value.Elem().FieldByName("Stream").Set(reflect.ValueOf(stream))
	return value
}

func errorFromValue(value reflect.Value) error {
	if value.IsNil() {
		return nil
	}
	return value.Interface().(error)
}
