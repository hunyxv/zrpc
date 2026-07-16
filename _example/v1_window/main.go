package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
	"github.com/hunyxv/zrpc/typed"
)

type UploadReq struct {
	Name string `msgpack:"name"`
}

type UploadResp struct {
	Count int `msgpack:"count"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const window = 96
	c := codec.Msgpack()
	chunk := chunkLargerThanHalfWindow(c, window)
	raw, err := c.Marshal(chunk)
	if err != nil {
		panic(err)
	}

	allowRecv := make(chan struct{})
	consumed := make(chan struct{})
	endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19204"}
	tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second, RouterMandatory: true, Immediate: true})
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: c, InitialStreamWindow: window})
	typed.HandleClientStream[UploadReq, UploadResp](srv, "upload.Window", func(ctx context.Context, stream *typed.ServerStream[UploadReq, UploadResp]) error {
		count := 0
		select {
		case <-allowRecv:
		case <-ctx.Done():
			return ctx.Err()
		}
		if _, err := stream.Recv(ctx); err != nil {
			return err
		}
		count++
		close(consumed)

		for {
			_, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				return stream.SendAndClose(ctx, &UploadResp{Count: count})
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

	cli := waitForClient(ctx, endpoint, tr, window, serverErr)
	defer func() { _ = cli.Close(context.Background()) }()

	stream, err := typed.NewClientStream[UploadReq, UploadResp](ctx, cli, "upload.Window")
	if err != nil {
		panic(err)
	}

	fmt.Printf("window size: %d bytes\n", window)
	fmt.Printf("chunk encoded size: %d bytes\n", len(raw))
	if err := stream.Send(ctx, chunk); err != nil {
		panic(err)
	}
	fmt.Println("send #1: ok")

	shortCtx, cancelShort := context.WithTimeout(ctx, 80*time.Millisecond)
	err = stream.Send(shortCtx, chunk)
	cancelShort()
	if !errors.Is(err, context.DeadlineExceeded) {
		panic(fmt.Sprintf("send #2 before server recv = %v, want context deadline exceeded", err))
	}
	fmt.Printf("send #2 before server recv: %v\n", err)

	close(allowRecv)
	select {
	case <-consumed:
	case <-time.After(time.Second):
		panic("server did not consume the first chunk")
	}
	fmt.Println("server consumed #1, window update sent")

	sendCtx, cancelSend := context.WithTimeout(ctx, time.Second)
	err = stream.Send(sendCtx, chunk)
	cancelSend()
	if err != nil {
		panic(err)
	}
	fmt.Println("send #2 after server recv, without client Recv: ok")

	resp, err := stream.CloseAndRecv(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Printf("server counted chunks: %d\n", resp.Count)
}

func waitForClient(ctx context.Context, endpoint transport.Endpoint, tr *zrpczmq.Transport, window int, serverErr <-chan error) *client.Client {
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: window})
		if err == nil {
			return cli
		}
		lastErr = err
		select {
		case err := <-serverErr:
			panic(err)
		case <-ctx.Done():
			panic(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	panic(lastErr)
}

func chunkLargerThanHalfWindow(c codec.Codec, window int) *UploadReq {
	for n := window; n > 0; n-- {
		chunk := &UploadReq{Name: strings.Repeat("x", n)}
		raw, err := c.Marshal(chunk)
		if err != nil {
			panic(err)
		}
		if len(raw) <= window && len(raw) > window/2 {
			return chunk
		}
	}
	panic("could not build chunk within stream window")
}
