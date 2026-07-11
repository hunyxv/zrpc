package typed

import (
	"context"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/client"
	"github.com/hunyxv/zrpc/server"
)

func HandleUnary[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *Req) (*Resp, error)) {
	srv.HandleUnary(method, zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
		var in Req
		if err := req.Decode(&in); err != nil {
			return nil, err
		}
		out, err := handler(ctx, &in)
		if err != nil {
			return nil, err
		}
		return zrpc.NewResponse(out, req.Codec)
	}))
}

func Invoke[Req any, Resp any](ctx context.Context, cli *client.Client, method string, req *Req) (*Resp, error) {
	rawResp, err := cli.Invoke(ctx, method, req)
	if err != nil {
		return nil, err
	}
	return decodeResponse[Resp](rawResp)
}

func decodeResponse[Resp any](rawResp *zrpc.Response) (*Resp, error) {
	var out Resp
	if err := rawResp.Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
