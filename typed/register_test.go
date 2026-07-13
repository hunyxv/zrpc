package typed

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

type reflectHelloService struct{}

func (reflectHelloService) Say(ctx context.Context, req *helloReq) (*helloResp, error) {
	return &helloResp{Message: "hello " + req.Name}, nil
}

type reflectRPCService struct{}

func (reflectRPCService) Say(ctx context.Context, req *helloReq) (*helloResp, error) {
	return &helloResp{Message: "hello " + req.Name}, nil
}

func (reflectRPCService) Upload(ctx context.Context, stream *ServerStream[helloReq, helloResp]) error {
	count := 0
	for {
		_, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(ctx, &helloResp{Message: strconv.Itoa(count)})
		}
		if err != nil {
			return err
		}
		count++
	}
}

func (reflectRPCService) List(ctx context.Context, req *helloReq, stream *ServerSender[helloResp]) error {
	for i := 0; i < 2; i++ {
		if err := stream.Send(ctx, &helloResp{Message: req.Name + "-" + strconv.Itoa(i)}); err != nil {
			return err
		}
	}
	return nil
}

func (reflectRPCService) Chat(ctx context.Context, stream *BidiServerStream[helloReq, helloResp]) error {
	for {
		req, err := stream.Recv(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, &helloResp{Message: "echo " + req.Name}); err != nil {
			return err
		}
	}
}

func TestRegisterServiceRegistersUnaryMethods(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-register-service"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	if err := RegisterService(srv, "hello", reflectHelloService{}); err != nil {
		t.Fatalf("RegisterService() error = %v", err)
	}
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := Invoke[helloReq, helloResp](context.Background(), cli, "hello.Say", &helloReq{Name: "reflect"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if resp.Message != "hello reflect" {
		t.Fatalf("message = %q", resp.Message)
	}
}

func TestRegisterServiceRegistersAllRPCShapes(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-register-all-shapes"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	if err := RegisterService(srv, "rpc", reflectRPCService{}); err != nil {
		t.Fatalf("RegisterService() error = %v", err)
	}
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()

	unaryResp, err := Invoke[helloReq, helloResp](context.Background(), cli, "rpc.Say", &helloReq{Name: "unary"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if unaryResp.Message != "hello unary" {
		t.Fatalf("unary message = %q", unaryResp.Message)
	}

	upload, err := NewClientStream[helloReq, helloResp](context.Background(), cli, "rpc.Upload")
	if err != nil {
		t.Fatalf("NewClientStream() error = %v", err)
	}
	if err := upload.Send(context.Background(), &helloReq{Name: "a"}); err != nil {
		t.Fatalf("Upload Send(a) error = %v", err)
	}
	if err := upload.Send(context.Background(), &helloReq{Name: "b"}); err != nil {
		t.Fatalf("Upload Send(b) error = %v", err)
	}
	uploadResp, err := upload.CloseAndRecv(context.Background())
	if err != nil {
		t.Fatalf("Upload CloseAndRecv() error = %v", err)
	}
	if uploadResp.Message != "2" {
		t.Fatalf("upload message = %q", uploadResp.Message)
	}

	list, err := NewServerStream[helloReq, helloResp](context.Background(), cli, "rpc.List", &helloReq{Name: "item"})
	if err != nil {
		t.Fatalf("NewServerStream() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		resp, err := list.Recv(context.Background())
		if err != nil {
			t.Fatalf("List Recv(%d) error = %v", i, err)
		}
		want := "item-" + strconv.Itoa(i)
		if resp.Message != want {
			t.Fatalf("list message = %q, want %q", resp.Message, want)
		}
	}
	if _, err := list.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("List Recv() after end error = %v, want EOF", err)
	}

	chat, err := NewBidiStream[helloReq, helloResp](context.Background(), cli, "rpc.Chat")
	if err != nil {
		t.Fatalf("NewBidiStream() error = %v", err)
	}
	for _, name := range []string{"first", "second"} {
		if err := chat.Send(context.Background(), &helloReq{Name: name}); err != nil {
			t.Fatalf("Chat Send(%s) error = %v", name, err)
		}
		resp, err := chat.Recv(context.Background())
		if err != nil {
			t.Fatalf("Chat Recv(%s) error = %v", name, err)
		}
		if resp.Message != "echo "+name {
			t.Fatalf("chat message = %q", resp.Message)
		}
	}
	if err := chat.CloseSend(context.Background()); err != nil {
		t.Fatalf("Chat CloseSend() error = %v", err)
	}
	if _, err := chat.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Chat Recv() after close error = %v, want EOF", err)
	}
}

func TestRegisterUsesConcreteTypeName(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-register"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
	if err := Register(srv, reflectHelloService{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
	defer func() { _ = cli.Close(context.Background()) }()
	resp, err := Invoke[helloReq, helloResp](context.Background(), cli, "reflectHelloService.Say", &helloReq{Name: "typed"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if resp.Message != "hello typed" {
		t.Fatalf("message = %q", resp.Message)
	}
}
