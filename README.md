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

## Unary 示例

```go
typed.HandleUnary[HelloReq, HelloResp](srv, "hello.Say", handler)

resp, err := typed.Invoke[HelloReq, HelloResp](
	ctx,
	cli,
	"hello.Say",
	&HelloReq{Name: "zrpc"},
)
```

完整示例见 [_example/v1_unary](./_example/v1_unary)。

## Client Streaming 示例

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
