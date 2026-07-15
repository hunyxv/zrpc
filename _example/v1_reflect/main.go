package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
	"github.com/hunyxv/zrpc/typed"
)

const serviceName = "demo"

// DemoReq 是示例请求体。
type DemoReq struct {
	Name string `msgpack:"name"`
}

// DemoResp 是示例响应体。
type DemoResp struct {
	Message string `msgpack:"message"`
}

// DemoServiceContract 是服务方 api 包可提供的服务端契约。
type DemoServiceContract interface {
	Say(context.Context, *DemoReq) (*DemoResp, error)
	Upload(context.Context, *typed.ServerStream[DemoReq, DemoResp]) error
	List(context.Context, *DemoReq, *typed.ServerSender[DemoResp]) error
	Chat(context.Context, *typed.BidiServerStream[DemoReq, DemoResp]) error
}

// DemoClient 是服务方 api 包可提供的客户端 proxy struct。
type DemoClient struct {
	Say func(context.Context, *DemoReq) (*DemoResp, error)

	Upload func(context.Context) (*typed.ClientStream[DemoReq, DemoResp], error)

	List func(context.Context, *DemoReq) (*typed.ServerStreamingClient[DemoResp], error)

	Chat func(context.Context) (*typed.BidiClientStream[DemoReq, DemoResp], error)

	SayAlias func(context.Context, *DemoReq) (*DemoResp, error) `zrpc:"Say"`

	LocalName string `zrpc:"-"`
}

// DemoService 展示通过反射语法糖注册四种 RPC 形态。
type DemoService struct{}

var _ DemoServiceContract = (*DemoService)(nil)

// Say 展示请求-响应调用。
func (DemoService) Say(ctx context.Context, req *DemoReq) (*DemoResp, error) {
	return &DemoResp{Message: "hello " + req.Name}, nil
}

// Upload 展示客户端流式请求。
func (DemoService) Upload(ctx context.Context, stream *typed.ServerStream[DemoReq, DemoResp]) error {
	count := 0
	for {
		_, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(ctx, &DemoResp{Message: strconv.Itoa(count)})
		}
		if err != nil {
			return err
		}
		count++
	}
}

// List 展示服务端流式响应。
func (DemoService) List(ctx context.Context, req *DemoReq, stream *typed.ServerSender[DemoResp]) error {
	for i := 0; i < 2; i++ {
		if err := stream.Send(ctx, &DemoResp{Message: req.Name + "-" + strconv.Itoa(i)}); err != nil {
			return err
		}
	}
	return nil
}

// Chat 展示双向流式调用。
func (DemoService) Chat(ctx context.Context, stream *typed.BidiServerStream[DemoReq, DemoResp]) error {
	for {
		req, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, &DemoResp{Message: "echo " + req.Name}); err != nil {
			return err
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19203"}
	tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second, RouterMandatory: true, Immediate: true})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	if err := typed.RegisterService(srv, serviceName, DemoService{}); err != nil {
		panic(err)
	}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ctx)
	}()

	cli := waitForClient(ctx, endpoint, tr, serverErr)
	defer func() { _ = cli.Close(context.Background()) }()

	demo := DemoClient{LocalName: "local-demo"}
	if err := typed.DecorateClient(cli, serviceName, &demo); err != nil {
		panic(err)
	}

	printUnary(ctx, &demo)
	printClientStream(ctx, &demo)
	printServerStream(ctx, &demo)
	printBidiStream(ctx, &demo)
}

func printUnary(ctx context.Context, demo *DemoClient) {
	resp, err := demo.Say(ctx, &DemoReq{Name: "zrpc"})
	if err != nil {
		panic(err)
	}
	fmt.Println("unary:", resp.Message)

	aliasResp, err := demo.SayAlias(ctx, &DemoReq{Name: demo.LocalName})
	if err != nil {
		panic(err)
	}
	fmt.Println("unary-alias:", aliasResp.Message)
}

func printClientStream(ctx context.Context, demo *DemoClient) {
	stream, err := demo.Upload(ctx)
	if err != nil {
		panic(err)
	}
	if err := stream.Send(ctx, &DemoReq{Name: "first"}); err != nil {
		panic(err)
	}
	if err := stream.Send(ctx, &DemoReq{Name: "second"}); err != nil {
		panic(err)
	}
	resp, err := stream.CloseAndRecv(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println("client-stream:", resp.Message)
}

func printServerStream(ctx context.Context, demo *DemoClient) {
	stream, err := demo.List(ctx, &DemoReq{Name: "item"})
	if err != nil {
		panic(err)
	}
	messages := []string{}
	for {
		resp, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			panic(err)
		}
		messages = append(messages, resp.Message)
	}
	fmt.Println("server-stream:", strings.Join(messages, ","))
}

func printBidiStream(ctx context.Context, demo *DemoClient) {
	stream, err := demo.Chat(ctx)
	if err != nil {
		panic(err)
	}
	messages := []string{}
	for _, name := range []string{"first", "second"} {
		if err := stream.Send(ctx, &DemoReq{Name: name}); err != nil {
			panic(err)
		}
		resp, err := stream.Recv(ctx)
		if err != nil {
			panic(err)
		}
		messages = append(messages, resp.Message)
	}
	if err := stream.CloseSend(ctx); err != nil {
		panic(err)
	}
	fmt.Println("bidi-stream:", strings.Join(messages, ","))
}

func waitForClient(ctx context.Context, endpoint transport.Endpoint, tr *zrpczmq.Transport, serverErr <-chan error) *client.Client {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-serverErr:
			panic(err)
		default:
		}
		cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
		if err == nil {
			return cli
		}
		lastErr = err
		if err := ctx.Err(); err != nil {
			panic(err)
		}
	}
	select {
	case err := <-serverErr:
		panic(err)
	default:
	}
	panic(lastErr)
}
