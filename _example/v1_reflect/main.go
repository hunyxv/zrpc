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

type DemoReq struct {
	Name string `msgpack:"name"`
}

type DemoResp struct {
	Message string `msgpack:"message"`
}

type DemoService struct{}

func (DemoService) Say(ctx context.Context, req *DemoReq) (*DemoResp, error) {
	return &DemoResp{Message: "hello " + req.Name}, nil
}

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

func (DemoService) List(ctx context.Context, req *DemoReq, stream *typed.ServerSender[DemoResp]) error {
	for i := 0; i < 2; i++ {
		if err := stream.Send(ctx, &DemoResp{Message: req.Name + "-" + strconv.Itoa(i)}); err != nil {
			return err
		}
	}
	return nil
}

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
	if err := typed.RegisterService(srv, "demo", DemoService{}); err != nil {
		panic(err)
	}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ctx)
	}()

	cli := waitForClient(ctx, endpoint, tr, serverErr)
	defer func() { _ = cli.Close(context.Background()) }()

	printUnary(ctx, cli)
	printClientStream(ctx, cli)
	printServerStream(ctx, cli)
	printBidiStream(ctx, cli)
}

func printUnary(ctx context.Context, cli *client.Client) {
	resp, err := typed.Invoke[DemoReq, DemoResp](ctx, cli, "demo.Say", &DemoReq{Name: "zrpc"})
	if err != nil {
		panic(err)
	}
	fmt.Println("unary:", resp.Message)
}

func printClientStream(ctx context.Context, cli *client.Client) {
	stream, err := typed.NewClientStream[DemoReq, DemoResp](ctx, cli, "demo.Upload")
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

func printServerStream(ctx context.Context, cli *client.Client) {
	stream, err := typed.NewServerStream[DemoReq, DemoResp](ctx, cli, "demo.List", &DemoReq{Name: "item"})
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

func printBidiStream(ctx context.Context, cli *client.Client) {
	stream, err := typed.NewBidiStream[DemoReq, DemoResp](ctx, cli, "demo.Chat")
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
