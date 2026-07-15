package zmq

import (
	"context"
	"errors"

	"github.com/hunyxv/zrpc/protocol"
	"github.com/hunyxv/zrpc/transport"
	zmq4 "github.com/pebbe/zmq4"
)

// Transport 是基于 ZeroMQ 的 zrpc transport 实现。
//
// 当前实现使用经典的 ROUTER/DEALER 组合：
//   - 服务端 Listen 创建一个 ROUTER socket，负责接收所有客户端 DEALER 消息。
//   - 客户端 Dial 创建一个 DEALER socket，每个连接独占一个 socket 和 owner goroutine。
//   - ZeroMQ socket 本身不允许多 goroutine 并发访问，所以所有 I/O 都经由 owner 串行化。
type Transport struct {
	opts Options
}

// New 创建 ZeroMQ transport。
//
// opts 会经过 defaultOptions 补默认值；传入零值即可得到可用配置。
func New(opts Options) *Transport {
	return &Transport{opts: defaultOptions(opts)}
}

// Name 返回 transport 名称。
func (t *Transport) Name() string {
	return "zmq"
}

// Dial 创建 DEALER socket 并连接到服务端 ROUTER。
//
// Dial 返回的是 zrpc 抽象 Conn。调用方通过 Conn.OpenStream 创建逻辑 stream，
// stream frame 再被编码为 ZeroMQ multipart message 发送给服务端 ROUTER。
func (t *Transport) Dial(ctx context.Context, endpoint transport.Endpoint, opts transport.DialOptions) (transport.Conn, error) {
	return newClientConn(ctx, endpoint, t.opts)
}

// Listen 创建 ROUTER socket 并监听 endpoint。
//
// 一个 ROUTER listener 可以承载多个客户端 DEALER，具体客户端由 ZeroMQ identity 区分。
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
	// 客户端使用 DEALER。DEALER 不需要显式指定 route，服务端 ROUTER 会在收到消息时
	// 自动获得该 DEALER 的 identity，并把它作为后续回复的路由地址。
	zctx, socket, err := newSocket(zmq4.DEALER, opts)
	if err != nil {
		return nil, err
	}
	// 为 DEALER 设置稳定 identity。服务端 ROUTER 看到的第一段消息就是这个 identity。
	// 当前 identity 只在本进程内递增生成，适合 Go-to-Go v1；未来集群/重连可扩展为更稳定的 client id。
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
	// local endpoint 使用 identity 作为地址，便于日志/追踪中定位本地逻辑连接。
	// remote endpoint 保留调用方传入的 ZeroMQ 地址，例如 tcp://127.0.0.1:5555。
	conn := newConn(id, transport.Endpoint{Transport: "zmq", Address: id}, endpoint, nil, false)
	// owner 拿到 socket 的独占访问权。收到服务端 frame 后直接路由到该 conn；
	// 客户端 DEALER 不需要 route，所以回调中的 route 参数会被忽略。
	owner := newOwner(zctx, socket, false, opts, func(route []byte, frame *protocol.Frame) {
		conn.routeFrame(frame)
	})
	conn.owner = owner

	handshakeCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()
	// 发送一次轻量 ping 触发 DEALER 与 ROUTER 建立可见通信路径。
	// 服务端收到 ping 后只创建/确认 route，不把它暴露成业务 stream。
	if err := owner.send(handshakeCtx, nil, &protocol.Frame{Type: protocol.FramePing}); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}
