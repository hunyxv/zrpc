# zrpc

zrpc 是一个 Go-to-Go RPC 框架。v1 采用显式的 `Client`、`Server`、`Handler` API，底层核心保持非泛型、稳定、可测试；对外通过 `typed` 包提供泛型 helper，获得更友好的业务侧调用体验。

## v1 特性

- 支持四种调用形态：请求-响应、流式请求、流式响应、双向流式。
- Transport 抽象，首个实现为 ZeroMQ，后续可扩展 HTTP/2、QUIC、TCP。
- 默认 codec 为 msgpack，内置 JSON codec 用于调试，保留 `Codec` 接口。
- 支持 unary/stream interceptor，可插入认证、日志、审计等中间件。
- 支持 OpenTelemetry trace context 注入和提取。
- 支持可插拔 metrics collector。
- 使用 RPC status code 作为错误模型，并支持 details。
- 内部提供 per-stream backpressure 基础能力。
- v1 单节点运行，保留 Resolver/Balancer seam，后续可添加集群和服务发现。

## v1 不包含

- 集群运行时。
- etcd、consul、zk 服务发现实现。
- 节点间转发。
- 跨语言 SDK。
- IDL/codegen。
- 自动 retry。
- ZeroMQ 传输层 TLS/mTLS/CURVE。
- Prometheus exporter。

## 安装要求

ZeroMQ transport 依赖 `libzmq` 和 CGO。macOS 可使用：

```sh
brew install zeromq
```

Linux 可使用发行版包管理器安装 `libzmq` 和对应开发头文件。

## 反射语法糖示例

推荐业务侧优先使用 `typed.RegisterService` 注册服务对象。它会按方法签名自动注册四种调用形态。
服务方建议提供独立的 `xxxapi` 契约包，只放请求/响应类型、服务名常量、服务端 interface 和客户端 proxy struct。

```go
package demoapi

const ServiceName = "demo"

type DemoReq struct {
	Name string
}

type DemoResp struct {
	Message string
}

type DemoService interface {
	Say(context.Context, *DemoReq) (*DemoResp, error)
	Upload(context.Context, *typed.ServerStream[DemoReq, DemoResp]) error
	List(context.Context, *DemoReq, *typed.ServerSender[DemoResp]) error
	Chat(context.Context, *typed.BidiServerStream[DemoReq, DemoResp]) error
}

type DemoClient struct {
	Say func(context.Context, *DemoReq) (*DemoResp, error)

	Upload func(context.Context) (*typed.ClientStream[DemoReq, DemoResp], error)

	List func(context.Context, *DemoReq) (*typed.ServerStreamingClient[DemoResp], error)

	Chat func(context.Context) (*typed.BidiClientStream[DemoReq, DemoResp], error)

	// 字段名是本地别名，真实 RPC 方法名由 tag 指定。
	SayAlias func(context.Context, *DemoReq) (*DemoResp, error) `zrpc:"Say"`

	// 显式跳过的字段可以由调用方自行初始化。
	LocalName string `zrpc:"-"`
}
```

服务端实现并注册：

```go
type DemoService struct{}

// 请求-响应：demo.Say
func (DemoService) Say(ctx context.Context, req *DemoReq) (*DemoResp, error) {
	return &DemoResp{Message: "hello " + req.Name}, nil
}

// 流式请求：demo.Upload
func (DemoService) Upload(ctx context.Context, stream *typed.ServerStream[DemoReq, DemoResp]) error {
	// 通过 stream.Recv 接收多条请求，最后 stream.SendAndClose 返回响应。
}

// 流式响应：demo.List
func (DemoService) List(ctx context.Context, req *DemoReq, stream *typed.ServerSender[DemoResp]) error {
	// 读取一个请求，通过 stream.Send 发送多条响应。
}

// 双向流式：demo.Chat
func (DemoService) Chat(ctx context.Context, stream *typed.BidiServerStream[DemoReq, DemoResp]) error {
	// 通过 stream.Recv / stream.Send 双向收发。
}

if err := typed.RegisterService(srv, "demo", DemoService{}); err != nil {
	return err
}
```

客户端装饰服务方提供的 proxy struct 后直接调用函数字段：

```go
demo := demoapi.DemoClient{LocalName: "local-demo"}
if err := typed.DecorateClient(cli, demoapi.ServiceName, &demo); err != nil {
	return err
}

unaryResp, err := demo.Say(ctx, req)
aliasResp, err := demo.SayAlias(ctx, req)

upload, err := demo.Upload(ctx)
err = upload.Send(ctx, &DemoReq{Name: "first"})
uploadResp, err := upload.CloseAndRecv(ctx)

list, err := demo.List(ctx, req)
listResp, err := list.Recv(ctx)

chat, err := demo.Chat(ctx)
err = chat.Send(ctx, &DemoReq{Name: "first"})
chatResp, err := chat.Recv(ctx)
```

完整四种调用示例见 [_example/v1_reflect](./_example/v1_reflect)。

## 普通显式 API 示例

普通显式方式只展示请求-响应和一个流式请求场景。请求-响应：

```go
typed.HandleUnary[HelloReq, HelloResp](srv, "hello.Say", handler)

resp, err := typed.Invoke[HelloReq, HelloResp](ctx, cli, "hello.Say", req)
```

完整示例见 [_example/v1_unary](./_example/v1_unary)。

流式请求：

```go
typed.HandleClientStream[UploadReq, UploadResp](srv, "upload.Count", handler)

stream, err := typed.NewClientStream[UploadReq, UploadResp](ctx, cli, "upload.Count")
if err != nil {
	return err
}
if err := stream.Send(ctx, &UploadReq{Name: "first"}); err != nil {
	return err
}
resp, err := stream.CloseAndRecv(ctx)
```

完整示例见 [_example/v1_stream](./_example/v1_stream)。

## Stream 背压示例

`InitialStreamWindow` 按编码后的 payload bytes 限制每条 stream 的在途数据量。接收方业务 `Recv` 消费一条 data frame 后，内部会发送 `FrameWindowUpdate(delta=len(payload))`；发送方后台 pump 会处理该控制帧并释放发送窗口。

[_example/v1_window](./_example/v1_window) 演示了慢消费者场景：客户端第二次 `Send` 会先因窗口不足超时；服务端消费第一条消息后，客户端不主动 `Recv` 也能继续 `Send`，证明后台 pump 已处理 window update。

## 核心 API

业务侧可以直接使用非泛型核心 API：

- `client.Client.Invoke`
- `client.Client.NewStream`
- `server.Server.HandleUnary`
- `server.Server.HandleStream`
- `zrpc.Request`
- `zrpc.Response`
- `zrpc.Stream`

`typed` 包只是薄封装，用于减少业务层重复的 encode/decode 代码。

## Transport

v1 内置：

- `transport/fake`：内存 transport，用于测试。
- `transport/zmq`：ZeroMQ ROUTER/DEALER transport。

ZeroMQ 使用 `transport.Endpoint` 描述地址：

```go
endpoint := transport.Endpoint{
	Transport: "zmq",
	Address:   "tcp://127.0.0.1:19201",
}
```

## 扩展点

- `codec.Codec`：自定义序列化协议。
- `interceptor.UnaryInterceptor` 和 `interceptor.StreamInterceptor`：中间件链。
- `metrics.Collector`：RPC、stream、transport 观测事件。
- `trace.Inject` / `trace.Extract`：OpenTelemetry metadata propagation。
- `security.Principal`：认证后身份信息放入 `context.Context`。
- `resolver.Resolver` / `balancer.Balancer`：后续集群和服务发现扩展点。

## 端到端测试

v1 ZeroMQ + typed API 的端到端测试位于 [testdata/v1](./testdata/v1)。
