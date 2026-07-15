package typed

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/server"
	"github.com/hunyxv/zrpc/transport"
	"github.com/hunyxv/zrpc/transport/fake"
)

type decoratedRPCClient struct {
	Say func(context.Context, *helloReq) (*helloResp, error)

	Upload func(context.Context) (*ClientStream[helloReq, helloResp], error)

	List func(context.Context, *helloReq) (*ServerStreamingClient[helloResp], error)

	Chat func(context.Context) (*BidiClientStream[helloReq, helloResp], error)

	SayAlias func(context.Context, *helloReq) (*helloResp, error) `zrpc:"Say"`

	Ignored string `zrpc:"-"`
	hidden  string
}

func TestDecorateClientDecoratesAllRPCShapes(t *testing.T) {
	tr := fake.New()
	endpoint := transport.Endpoint{Transport: "fake", Address: "typed-decorate-client"}
	srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	if err := RegisterService(srv, "rpc", reflectRPCService{}); err != nil {
		t.Fatalf("RegisterService() error = %v", err)
	}
	run := startServer(t, srv)
	defer run.cancel()

	cli := waitForClient(t, client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
	defer func() { _ = cli.Close(context.Background()) }()

	proxy := decoratedRPCClient{Ignored: "custom", hidden: "private"}
	if err := DecorateClient(cli, "rpc", &proxy); err != nil {
		t.Fatalf("DecorateClient() error = %v", err)
	}
	if proxy.Ignored != "custom" {
		t.Fatalf("Ignored = %q, want custom", proxy.Ignored)
	}
	if proxy.hidden != "private" {
		t.Fatalf("hidden = %q, want private", proxy.hidden)
	}

	unaryResp, err := proxy.Say(context.Background(), &helloReq{Name: "unary"})
	if err != nil {
		t.Fatalf("Say() error = %v", err)
	}
	if unaryResp.Message != "hello unary" {
		t.Fatalf("Say() message = %q", unaryResp.Message)
	}

	aliasResp, err := proxy.SayAlias(context.Background(), &helloReq{Name: "alias"})
	if err != nil {
		t.Fatalf("SayAlias() error = %v", err)
	}
	if aliasResp.Message != "hello alias" {
		t.Fatalf("SayAlias() message = %q", aliasResp.Message)
	}

	upload, err := proxy.Upload(context.Background())
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := upload.Send(context.Background(), &helloReq{Name: name}); err != nil {
			t.Fatalf("Upload Send(%s) error = %v", name, err)
		}
	}
	uploadResp, err := upload.CloseAndRecv(context.Background())
	if err != nil {
		t.Fatalf("Upload CloseAndRecv() error = %v", err)
	}
	if uploadResp.Message != "3" {
		t.Fatalf("Upload message = %q, want 3", uploadResp.Message)
	}

	list, err := proxy.List(context.Background(), &helloReq{Name: "item"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		resp, err := list.Recv(context.Background())
		if err != nil {
			t.Fatalf("List Recv(%d) error = %v", i, err)
		}
		want := "item-" + strconv.Itoa(i)
		if resp.Message != want {
			t.Fatalf("List message = %q, want %q", resp.Message, want)
		}
	}
	if _, err := list.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("List Recv() after end error = %v, want EOF", err)
	}

	chat, err := proxy.Chat(context.Background())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
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
			t.Fatalf("Chat message = %q", resp.Message)
		}
	}
	if err := chat.CloseSend(context.Background()); err != nil {
		t.Fatalf("Chat CloseSend() error = %v", err)
	}
	if _, err := chat.Recv(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Chat Recv() after close error = %v, want EOF", err)
	}
}

func TestDecorateClientValidatesProxyBeforeDecorating(t *testing.T) {
	type invalidProxy struct {
		Say func(context.Context, *helloReq) (*helloResp, error)
		Bad int
	}

	proxy := invalidProxy{}
	err := DecorateClient(&client.Client{}, "rpc", &proxy)
	if err == nil {
		t.Fatal("DecorateClient() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Bad") {
		t.Fatalf("DecorateClient() error = %v, want field name", err)
	}
	if proxy.Say != nil {
		t.Fatal("Say was decorated after validation error")
	}
}

func TestDecorateClientRejectsUnsupportedFunctionSignature(t *testing.T) {
	type invalidProxy struct {
		Say func(context.Context, helloReq) (*helloResp, error)
	}

	err := DecorateClient(&client.Client{}, "rpc", &invalidProxy{})
	if err == nil {
		t.Fatal("DecorateClient() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Say") || !strings.Contains(err.Error(), "unsupported signature") {
		t.Fatalf("DecorateClient() error = %v, want unsupported signature with field name", err)
	}
}

func TestDecorateClientRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		cli         *client.Client
		serviceName string
		proxy       any
	}{
		{name: "nil client", cli: nil, serviceName: "rpc", proxy: &decoratedRPCClient{}},
		{name: "empty service name", cli: &client.Client{}, serviceName: "", proxy: &decoratedRPCClient{}},
		{name: "nil proxy", cli: &client.Client{}, serviceName: "rpc", proxy: nil},
		{name: "non pointer proxy", cli: &client.Client{}, serviceName: "rpc", proxy: decoratedRPCClient{}},
		{name: "non struct pointer", cli: &client.Client{}, serviceName: "rpc", proxy: new(int)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := DecorateClient(tt.cli, tt.serviceName, tt.proxy); err == nil {
				t.Fatal("DecorateClient() error = nil, want error")
			}
		})
	}
}
