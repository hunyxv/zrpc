package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
	"github.com/hunyxv/zrpc/typed"
)

// UploadReq 是流式上传示例的请求体。
type UploadReq struct {
	Name string `msgpack:"name"`
}

// UploadResp 是流式上传示例的响应体。
type UploadResp struct {
	Message string `msgpack:"message"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19202"}
	tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second, RouterMandatory: true, Immediate: true})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	typed.HandleClientStream[UploadReq, UploadResp](srv, "upload.Count", func(ctx context.Context, stream *typed.ServerStream[UploadReq, UploadResp]) error {
		count := 0
		for {
			_, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				return stream.SendAndClose(ctx, &UploadResp{Message: strconv.Itoa(count)})
			}
			if err != nil {
				return err
			}
			count++
		}
	})
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ctx)
	}()

	cli := waitForClient(ctx, endpoint, tr, serverErr)
	defer func() { _ = cli.Close(context.Background()) }()
	stream, err := typed.NewClientStream[UploadReq, UploadResp](ctx, cli, "upload.Count")
	if err != nil {
		panic(err)
	}
	if err := stream.Send(ctx, &UploadReq{Name: "first"}); err != nil {
		panic(err)
	}
	if err := stream.Send(ctx, &UploadReq{Name: "second"}); err != nil {
		panic(err)
	}
	resp, err := stream.CloseAndRecv(ctx)
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
