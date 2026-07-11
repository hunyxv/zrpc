package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
	"github.com/hunyxv/zrpc/typed"
)

type HelloReq struct {
	Name string `msgpack:"name"`
}

type HelloResp struct {
	Message string `msgpack:"message"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19201"}
	tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second, RouterMandatory: true, Immediate: true})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	typed.HandleUnary[HelloReq, HelloResp](srv, "hello.Say", func(ctx context.Context, req *HelloReq) (*HelloResp, error) {
		return &HelloResp{Message: "hello " + req.Name}, nil
	})
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ctx)
	}()

	cli := waitForClient(ctx, endpoint, tr, serverErr)
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := typed.Invoke[HelloReq, HelloResp](ctx, cli, "hello.Say", &HelloReq{Name: "zrpc"})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Message)
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
		cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
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
