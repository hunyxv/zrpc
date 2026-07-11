package zmq

import (
	"context"
	"errors"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

type Transport struct {
	opts Options
}

func New(opts Options) *Transport {
	return &Transport{opts: defaultOptions(opts)}
}

func (t *Transport) Name() string {
	return "zmq"
}

func (t *Transport) Dial(ctx context.Context, endpoint transport.Endpoint, opts transport.DialOptions) (transport.Conn, error) {
	return newClientConn(ctx, endpoint, t.opts)
}

func (t *Transport) Listen(endpoint transport.Endpoint, opts transport.ListenOptions) (transport.Listener, error) {
	return newListener(endpoint, t.opts)
}

func newClientConn(ctx context.Context, endpoint transport.Endpoint, opts Options) (transport.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if endpoint.Address == "" {
		return nil, errors.New("zmq: endpoint address is required")
	}
	endpoint = normalizeEndpoint(endpoint)
	id := nextConnID("client")
	zctx, socket, err := newSocket(zmq4.DEALER, opts)
	if err != nil {
		return nil, err
	}
	if err := socket.SetIdentity(id); err != nil {
		_ = socket.Close()
		_ = zctx.Term()
		return nil, err
	}
	if err := socket.Connect(endpoint.Address); err != nil {
		_ = socket.Close()
		_ = zctx.Term()
		return nil, err
	}
	conn := newConn(id, transport.Endpoint{Transport: "zmq", Address: id}, endpoint, nil, false)
	owner := newOwner(zctx, socket, false, opts, func(route []byte, frame *protocol.Frame) {
		conn.routeFrame(frame)
	})
	conn.owner = owner

	handshakeCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()
	if err := owner.send(handshakeCtx, nil, &protocol.Frame{Type: protocol.FramePing}); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}
