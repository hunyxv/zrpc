package typed

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
)

type decoratedClientMethodKind int

const (
	decoratedUnary decoratedClientMethodKind = iota + 1
	decoratedClientStream
	decoratedServerStream
	decoratedBidiStream
)

type decoratedClientMethod struct {
	fieldIndex int
	method     string
	kind       decoratedClientMethodKind
	funcType   reflect.Type
}

// DecorateClient 将 proxy struct 中符合约定的函数字段装饰成 RPC 客户端调用。
//
// proxy 必须是非 nil 的 *struct。未导出字段会被忽略；导出的非函数字段必须显式标记
// zrpc:"-" 才会被跳过。导出的函数字段支持四种客户端签名：
//   - func(context.Context, *Req) (*Resp, error)
//   - func(context.Context) (*ClientStream[Req, Resp], error)
//   - func(context.Context, *Req) (*ServerStreamingClient[Resp], error)
//   - func(context.Context) (*BidiClientStream[Req, Resp], error)
func DecorateClient(cli *client.Client, serviceName string, proxy any) error {
	if cli == nil {
		return errors.New("zrpc/typed: client is nil")
	}
	if serviceName == "" {
		return errors.New("zrpc/typed: service name is required")
	}
	value, err := clientProxyValue(proxy)
	if err != nil {
		return err
	}
	methods, err := collectDecoratedClientMethods(value.Type(), serviceName)
	if err != nil {
		return err
	}
	if len(methods) == 0 {
		return fmt.Errorf("zrpc/typed: decorate client %q has no RPC function fields", serviceName)
	}
	for _, method := range methods {
		field := value.Field(method.fieldIndex)
		field.Set(makeDecoratedClientFunc(cli, method))
	}
	return nil
}

func clientProxyValue(proxy any) (reflect.Value, error) {
	value := reflect.ValueOf(proxy)
	if !value.IsValid() {
		return reflect.Value{}, errors.New("zrpc/typed: proxy is nil")
	}
	if value.Kind() != reflect.Ptr {
		return reflect.Value{}, errors.New("zrpc/typed: proxy must be pointer to struct")
	}
	if value.IsNil() {
		return reflect.Value{}, errors.New("zrpc/typed: proxy is nil")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return reflect.Value{}, errors.New("zrpc/typed: proxy must be pointer to struct")
	}
	return value, nil
}

func collectDecoratedClientMethods(proxyType reflect.Type, serviceName string) ([]decoratedClientMethod, error) {
	methods := make([]decoratedClientMethod, 0, proxyType.NumField())
	for i := 0; i < proxyType.NumField(); i++ {
		field := proxyType.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("zrpc")
		if tag == "-" {
			continue
		}
		if field.Type.Kind() != reflect.Func {
			return nil, fmt.Errorf("zrpc/typed: decorate client %s.%s: exported non-function field must use zrpc:\"-\"", serviceName, field.Name)
		}
		kind, err := decoratedClientMethodKindFor(field.Type)
		if err != nil {
			return nil, fmt.Errorf("zrpc/typed: decorate client %s.%s: %w", serviceName, field.Name, err)
		}
		methodName := field.Name
		if tag != "" {
			methodName = tag
		}
		methods = append(methods, decoratedClientMethod{
			fieldIndex: i,
			method:     serviceName + "." + methodName,
			kind:       kind,
			funcType:   field.Type,
		})
	}
	return methods, nil
}

func decoratedClientMethodKindFor(funcType reflect.Type) (decoratedClientMethodKind, error) {
	if funcType.NumIn() == 0 || funcType.In(0) != contextType {
		return 0, errors.New("unsupported signature: first parameter must be context.Context")
	}
	if funcType.NumOut() != 2 || funcType.Out(1) != errorType {
		return 0, errors.New("unsupported signature: returns must end with error")
	}
	switch {
	case isDecoratedClientStreamMethod(funcType):
		return decoratedClientStream, nil
	case isDecoratedBidiStreamMethod(funcType):
		return decoratedBidiStream, nil
	case isDecoratedServerStreamMethod(funcType):
		return decoratedServerStream, nil
	case isDecoratedUnaryMethod(funcType):
		return decoratedUnary, nil
	default:
		return 0, fmt.Errorf("unsupported signature %s", funcType.String())
	}
}

func isDecoratedUnaryMethod(funcType reflect.Type) bool {
	return funcType.NumIn() == 2 &&
		funcType.In(1).Kind() == reflect.Ptr &&
		funcType.Out(0).Kind() == reflect.Ptr
}

func isDecoratedClientStreamMethod(funcType reflect.Type) bool {
	return funcType.NumIn() == 1 && isTypedStream(funcType.Out(0), "ClientStream")
}

func isDecoratedServerStreamMethod(funcType reflect.Type) bool {
	return funcType.NumIn() == 2 &&
		funcType.In(1).Kind() == reflect.Ptr &&
		isTypedStream(funcType.Out(0), "ServerStreamingClient")
}

func isDecoratedBidiStreamMethod(funcType reflect.Type) bool {
	return funcType.NumIn() == 1 && isTypedStream(funcType.Out(0), "BidiClientStream")
}

func makeDecoratedClientFunc(cli *client.Client, method decoratedClientMethod) reflect.Value {
	return reflect.MakeFunc(method.funcType, func(args []reflect.Value) []reflect.Value {
		switch method.kind {
		case decoratedUnary:
			return callDecoratedUnary(cli, method, args)
		case decoratedClientStream, decoratedBidiStream:
			return openDecoratedStream(cli, method, args[0])
		case decoratedServerStream:
			return openDecoratedServerStream(cli, method, args)
		default:
			return decoratedClientErrorResults(method.funcType, errors.New("zrpc/typed: unknown decorated client method kind"))
		}
	})
}

func callDecoratedUnary(cli *client.Client, method decoratedClientMethod, args []reflect.Value) []reflect.Value {
	resp, err := cli.Invoke(args[0].Interface().(context.Context), method.method, args[1].Interface())
	if err != nil {
		return decoratedClientErrorResults(method.funcType, err)
	}
	out := reflect.New(method.funcType.Out(0).Elem())
	if err := resp.Decode(out.Interface()); err != nil {
		return decoratedClientErrorResults(method.funcType, err)
	}
	return []reflect.Value{out, reflect.Zero(errorType)}
}

func openDecoratedStream(cli *client.Client, method decoratedClientMethod, ctxValue reflect.Value) []reflect.Value {
	stream, err := cli.NewStream(ctxValue.Interface().(context.Context), method.method)
	if err != nil {
		return decoratedClientErrorResults(method.funcType, err)
	}
	return []reflect.Value{newDecoratedClientStream(method.funcType.Out(0), stream), reflect.Zero(errorType)}
}

func openDecoratedServerStream(cli *client.Client, method decoratedClientMethod, args []reflect.Value) []reflect.Value {
	ctx := args[0].Interface().(context.Context)
	stream, err := cli.NewStream(ctx, method.method)
	if err != nil {
		return decoratedClientErrorResults(method.funcType, err)
	}
	if err := stream.Send(ctx, args[1].Interface()); err != nil {
		_ = stream.Reset(ctx, err)
		return decoratedClientErrorResults(method.funcType, err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		return decoratedClientErrorResults(method.funcType, err)
	}
	return []reflect.Value{newDecoratedClientStream(method.funcType.Out(0), stream), reflect.Zero(errorType)}
}

func newDecoratedClientStream(typ reflect.Type, stream zrpc.Stream) reflect.Value {
	value := reflect.New(typ.Elem())
	value.Elem().FieldByName("Stream").Set(reflect.ValueOf(stream))
	return value
}

func decoratedClientErrorResults(funcType reflect.Type, err error) []reflect.Value {
	results := make([]reflect.Value, funcType.NumOut())
	for i := 0; i < funcType.NumOut()-1; i++ {
		results[i] = reflect.Zero(funcType.Out(i))
	}
	results[funcType.NumOut()-1] = reflect.ValueOf(err)
	return results
}
