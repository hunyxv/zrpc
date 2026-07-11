package client

import (
	"context"
	"errors"

	"github.com/hunyxv/zrpc"
	"github.com/hunyxv/zrpc/balancer"
	"github.com/hunyxv/zrpc/metadata"
	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/resolver"
	"github.com/hunyxv/zrpc/status"
	"github.com/hunyxv/zrpc/transport"
)

type Client struct {
	opts Options
	conn transport.Conn
}

func New(opts Options) (*Client, error) {
	if opts.Transport == nil {
		return nil, errors.New("zrpc/client: transport is required")
	}
	if opts.Codec == nil {
		return nil, errors.New("zrpc/client: codec is required")
	}
	r := opts.Resolver
	if r == nil {
		r = resolver.Static(opts.Target)
	}
	b := opts.Balancer
	if b == nil {
		b = balancer.PickFirst()
	}
	ctx := context.Background()
	endpoints, err := r.Resolve(ctx, opts.Target.Address)
	if err != nil {
		return nil, err
	}
	endpoint, err := b.Pick(ctx, endpoints)
	if err != nil {
		return nil, err
	}
	conn, err := opts.Transport.Dial(ctx, endpoint, transport.DialOptions{})
	if err != nil {
		return nil, err
	}
	return &Client{opts: opts, conn: conn}, nil
}

func (c *Client) Invoke(ctx context.Context, method string, value any) (*zrpc.Response, error) {
	req, err := zrpc.NewRequest(method, value, c.opts.Codec)
	if err != nil {
		return nil, err
	}
	stream, err := c.conn.OpenStream(ctx, method, req.Metadata)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.Close(context.Background())
	}()
	if err := stream.SendFrame(ctx, &protocol.Frame{
		Type:      protocol.FrameData,
		StreamID:  stream.ID(),
		Direction: protocol.DirectionClientToServer,
		Payload:   req.Body,
	}); err != nil {
		_ = stream.Reset(ctx, &status.Status{Code: status.Unknown, Message: err.Error()})
		return nil, err
	}

	respFrame, err := stream.RecvFrame(ctx)
	if err != nil {
		return nil, err
	}
	if respFrame.Type == protocol.FrameReset {
		if respFrame.Status != nil {
			return nil, status.WithDetails(status.Error(respFrame.Status.Code, respFrame.Status.Message), respFrame.Status.Details...)
		}
		return nil, status.Error(status.Unknown, "stream reset")
	}
	if respFrame.Type != protocol.FrameResponse {
		return nil, status.Error(status.Internal, "unexpected response frame")
	}
	st := status.Status{Code: status.OK}
	if respFrame.Status != nil {
		st = *respFrame.Status
	}
	if st.Code != status.OK {
		return nil, status.WithDetails(status.Error(st.Code, st.Message), st.Details...)
	}
	return zrpc.NewResponseBytes(respFrame.Metadata, respFrame.Payload, c.opts.Codec)
}

func (c *Client) NewStream(ctx context.Context, method string) (zrpc.Stream, error) {
	md := metadata.New()
	md.Set(transport.ModeMetadataKey, transport.ModeStream)
	stream, err := c.conn.OpenStream(ctx, method, md)
	if err != nil {
		return nil, err
	}
	return zrpc.NewInternalStream(ctx, method, md, c.opts.Codec, stream, c.opts.InitialStreamWindow, protocol.DirectionClientToServer), nil
}

func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close(ctx)
}
