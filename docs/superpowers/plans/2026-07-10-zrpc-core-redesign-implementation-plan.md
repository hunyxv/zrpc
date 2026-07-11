# zrpc Core Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 zrpc 重构为一个 Go-to-Go、高并发、可测试、可扩展的 RPC 框架，v1 支持 unary、client streaming、server streaming、bidirectional streaming，并以 ZeroMQ 作为首个 transport。

**Architecture:** 核心采用显式 Client/Server/Handler API，业务侧通过泛型 helper 获得类型友好体验；协议、codec、metadata、status、middleware、transport 分层解耦。v1 不实现集群运行时，但 client 创建连接时必须经过 Resolver/Balancer seam，后续可扩展 etcd/consul/zk 和多节点负载均衡。

**Tech Stack:** Go 1.25.0、ZeroMQ/libzmq、github.com/pebbe/zmq4、github.com/vmihailenco/msgpack/v5、OpenTelemetry、标准库 `context`/`sync`/`testing`。

---

## 执行约束

- 不追求兼容旧 API。
- 不在旧 `broker`、`multiplexer`、`methodfunc`、`client/channel` 上打补丁式演进。
- 先创建新分层包，再逐步替换 README 和示例。
- 每个任务先写测试，再实现。
- 每个任务完成后运行对应测试并提交。
- 项目 Go 版本固定为 `go1.25.0`；所有实现、测试、`go.mod` 都按 Go 1.25.0 处理。
- v1 不实现集群、服务发现实现、节点间转发、自动 retry、传输层 TLS/mTLS、Prometheus exporter。
- 当前工作区存在 staged `.gitignore`，执行本计划时不要误提交它，除非用户明确要求。

## 目标文件结构

### 基础层

- Create: `codec/codec.go`  
  定义 `Codec` 接口。
- Create: `codec/msgpack.go`  
  实现默认 msgpack codec。
- Create: `codec/json.go`  
  实现调试 json codec。
- Create: `codec/codec_test.go`  
  覆盖 msgpack/json round trip、decode error。
- Create: `metadata/metadata.go`  
  定义 `MD`、copy、merge、get/set、append。
- Create: `metadata/metadata_test.go`  
  覆盖 metadata 不共享底层 slice、大小写策略、merge 行为。
- Create: `status/code.go`  
  定义 RPC code。
- Create: `status/status.go`  
  定义 `Status`、`Error`、`FromError`、`WithDetails`。
- Create: `status/status_test.go`  
  覆盖 code 映射、error round trip、details。

### 协议层

- Create: `protocol/frame.go`  
  定义 `FrameType`、`Direction`、`Frame`。
- Create: `protocol/session.go`  
  定义 stream session 状态、half-close 状态机。
- Create: `protocol/window.go`  
  定义 byte window、in-flight 计数、window update。
- Create: `protocol/frame_test.go`  
  覆盖 frame 编解码和必要字段校验。
- Create: `protocol/session_test.go`  
  覆盖 stream open/end/reset/half-close。
- Create: `protocol/window_test.go`  
  覆盖 send window、recv window、backpressure、超限。

### Transport 层

- Create: `transport/types.go`  
  定义 `Endpoint`、`Transport`、`Listener`、`Conn`、`TransportStream`。
- Create: `transport/options.go`  
  定义 `DialOptions`、`ListenOptions`、`ConnOptions`。
- Create: `transport/fake/fake.go`  
  实现内存 fake transport，用于核心 client/server 测试。
- Create: `transport/fake/fake_test.go`  
  覆盖 open stream、accept stream、send error、recv close。
- Create: `transport/zmq/options.go`  
  定义 ZeroMQ transport 配置。
- Create: `transport/zmq/client.go`  
  实现 client DEALER transport。
- Create: `transport/zmq/server.go`  
  实现 server ROUTER listener。
- Create: `transport/zmq/conn.go`  
  实现 ZeroMQ conn、owner goroutine、stream dispatch。
- Create: `transport/zmq/zmq_test.go`  
  覆盖 ZeroMQ unary frame round trip、close、send failure。

### RPC 核心层

- Create: `request.go`  
  定义 `Request`、`Response`、encode/decode helper。
- Create: `stream.go`  
  定义核心 `Stream` 接口和实现。
- Create: `handler.go`  
  定义 unary/stream handler 接口。
- Create: `server/server.go`  
  定义 `Server`、handler registry、Serve、GracefulStop。
- Create: `server/options.go`  
  定义 server options。
- Create: `client/client.go`  
  定义 `Client`、Invoke、NewStream、Close。
- Create: `client/options.go`  
  定义 client options。
- Create: `client/client_test.go`  
  基于 fake transport 覆盖 unary、错误、cancel。
- Create: `server/server_test.go`  
  基于 fake transport 覆盖注册、unknown method、panic recover。
- Create: `stream_test.go`  
  基于 fake transport 覆盖三种 stream 模式。

### 扩展层

- Create: `interceptor/interceptor.go`  
  定义 unary/stream interceptor 和 chain。
- Create: `interceptor/interceptor_test.go`  
  覆盖执行顺序、短路、panic recover。
- Create: `metrics/metrics.go`  
  定义 Collector、NoopCollector、事件类型。
- Create: `metrics/metrics_test.go`  
  覆盖 noop 和 collector 调用点。
- Create: `trace/trace.go`  
  定义 OpenTelemetry 注入/提取和 span 创建 helper。
- Create: `trace/trace_test.go`  
  覆盖 trace metadata propagation。
- Create: `security/principal.go`  
  定义 Principal context helper。
- Create: `security/principal_test.go`  
  覆盖 principal 注入和读取。
- Create: `resolver/resolver.go`  
  定义 Resolver、Update、StaticResolver。
- Create: `resolver/resolver_test.go`  
  覆盖静态 endpoint resolve/watch。
- Create: `balancer/balancer.go`  
  定义 Balancer、PickFirstBalancer。
- Create: `balancer/balancer_test.go`  
  覆盖空 endpoint、单 endpoint、多 endpoint pick-first。
- Create: `typed/unary.go`  
  定义泛型 unary helper。
- Create: `typed/stream.go`  
  定义泛型 stream helper。
- Create: `typed/typed_test.go`  
  覆盖 typed helper 的 encode/decode 和错误透传。

### 示例和文档

- Replace: `README.md`  
  改为新 API 文档。
- Create: `_example/v1_unary/main.go`  
  新 unary 示例。
- Create: `_example/v1_stream/main.go`  
  新 stream 示例。
- Create: `docs/superpowers/plans/2026-07-10-zrpc-core-redesign-implementation-plan.md`  
  本实施计划。

## Task 1: 基础类型与 codec/status/metadata

**Files:**
- Modify: `go.mod`
- Create: `codec/codec.go`
- Create: `codec/msgpack.go`
- Create: `codec/json.go`
- Create: `codec/codec_test.go`
- Create: `metadata/metadata.go`
- Create: `metadata/metadata_test.go`
- Create: `status/code.go`
- Create: `status/status.go`
- Create: `status/status_test.go`

- [ ] **Step 1: 更新 go.mod Go 版本**

Modify `go.mod`:

```go
module github.com/hunyxv/zrpc

go 1.25.0
```

保留原有 `require` 块，后续任务通过 `rtk go mod tidy` 删除不再需要的旧集群依赖。

- [ ] **Step 2: 确认本机 Go 版本**

Run: `rtk go version`
Expected: 输出包含 `go1.25.0`。

- [ ] **Step 3: 写 codec 失败测试**

Create `codec/codec_test.go`:

```go
package codec

import "testing"

type samplePayload struct {
    ID   string `json:"id" msgpack:"id"`
    Name string `json:"name" msgpack:"name"`
}

func TestMsgpackRoundTrip(t *testing.T) {
    c := Msgpack()
    in := samplePayload{ID: "42", Name: "zrpc"}
    raw, err := c.Marshal(in)
    if err != nil {
        t.Fatalf("Marshal() error = %v", err)
    }
    var out samplePayload
    if err := c.Unmarshal(raw, &out); err != nil {
        t.Fatalf("Unmarshal() error = %v", err)
    }
    if out != in {
        t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
    }
}

func TestJSONRoundTrip(t *testing.T) {
    c := JSON()
    in := samplePayload{ID: "7", Name: "debug"}
    raw, err := c.Marshal(in)
    if err != nil {
        t.Fatalf("Marshal() error = %v", err)
    }
    var out samplePayload
    if err := c.Unmarshal(raw, &out); err != nil {
        t.Fatalf("Unmarshal() error = %v", err)
    }
    if out != in {
        t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
    }
}
```

- [ ] **Step 4: 运行 codec 测试确认失败**

Run: `rtk go test ./codec`  
Expected: FAIL，错误包含 `undefined: Msgpack` 或 `undefined: JSON`。

- [ ] **Step 5: 实现 codec 最小代码**

Create `codec/codec.go`:

```go
package codec

type Codec interface {
    Name() string
    Marshal(v any) ([]byte, error)
    Unmarshal(data []byte, v any) error
}
```

Create `codec/msgpack.go`:

```go
package codec

import "github.com/vmihailenco/msgpack/v5"

type msgpackCodec struct{}

func Msgpack() Codec { return msgpackCodec{} }

func (msgpackCodec) Name() string { return "msgpack" }

func (msgpackCodec) Marshal(v any) ([]byte, error) {
    return msgpack.Marshal(v)
}

func (msgpackCodec) Unmarshal(data []byte, v any) error {
    return msgpack.Unmarshal(data, v)
}
```

Create `codec/json.go`:

```go
package codec

import "encoding/json"

type jsonCodec struct{}

func JSON() Codec { return jsonCodec{} }

func (jsonCodec) Name() string { return "json" }

func (jsonCodec) Marshal(v any) ([]byte, error) {
    return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
    return json.Unmarshal(data, v)
}
```

- [ ] **Step 6: 运行 codec 测试确认通过**

Run: `rtk go test ./codec`  
Expected: PASS。

- [ ] **Step 7: 写 metadata 失败测试**

Create `metadata/metadata_test.go`:

```go
package metadata

import "testing"

func TestMetadataSetGetAppendCopy(t *testing.T) {
    md := New()
    md.Set("authorization", "Bearer token")
    md.Append("x-request-id", "a")
    md.Append("x-request-id", "b")
    if got := md.Get("authorization"); got != "Bearer token" {
        t.Fatalf("authorization = %q", got)
    }
    gotValues := md.Values("x-request-id")
    if len(gotValues) != 2 || gotValues[0] != "a" || gotValues[1] != "b" {
        t.Fatalf("x-request-id values = %#v", gotValues)
    }
    copied := md.Copy()
    copied.Set("authorization", "changed")
    if got := md.Get("authorization"); got != "Bearer token" {
        t.Fatalf("copy shared backing storage, original = %q", got)
    }
}

func TestMetadataMerge(t *testing.T) {
    left := New()
    left.Set("a", "1")
    right := New()
    right.Set("b", "2")
    merged := Merge(left, right)
    if merged.Get("a") != "1" || merged.Get("b") != "2" {
        t.Fatalf("merged = %#v", merged)
    }
}
```

- [ ] **Step 8: 运行 metadata 测试确认失败**

Run: `rtk go test ./metadata`  
Expected: FAIL，错误包含 `undefined: New`。

- [ ] **Step 9: 实现 metadata**

Create `metadata/metadata.go`:

```go
package metadata

type MD map[string][]string

func New() MD {
    return MD{}
}

func (md MD) Set(key, value string) {
    md[key] = []string{value}
}

func (md MD) Append(key, value string) {
    md[key] = append(md[key], value)
}

func (md MD) Get(key string) string {
    values := md[key]
    if len(values) == 0 {
        return ""
    }
    return values[0]
}

func (md MD) Values(key string) []string {
    values := md[key]
    out := make([]string, len(values))
    copy(out, values)
    return out
}

func (md MD) Copy() MD {
    out := New()
    for key, values := range md {
        copied := make([]string, len(values))
        copy(copied, values)
        out[key] = copied
    }
    return out
}

func Merge(items ...MD) MD {
    out := New()
    for _, item := range items {
        for key, values := range item {
            for _, value := range values {
                out.Append(key, value)
            }
        }
    }
    return out
}
```

- [ ] **Step 10: 运行 metadata 测试确认通过**

Run: `rtk go test ./metadata`  
Expected: PASS。

- [ ] **Step 11: 写 status 失败测试**

Create `status/status_test.go`:

```go
package status

import (
    "errors"
    "testing"
)

func TestStatusErrorRoundTrip(t *testing.T) {
    err := Error(Unavailable, "transport closed")
    st := FromError(err)
    if st.Code != Unavailable {
        t.Fatalf("code = %v", st.Code)
    }
    if st.Message != "transport closed" {
        t.Fatalf("message = %q", st.Message)
    }
}

func TestUnknownForPlainError(t *testing.T) {
    st := FromError(errors.New("plain"))
    if st.Code != Unknown {
        t.Fatalf("code = %v", st.Code)
    }
    if st.Message != "plain" {
        t.Fatalf("message = %q", st.Message)
    }
}

func TestWithDetails(t *testing.T) {
    err := WithDetails(Error(InvalidArgument, "bad request"), "field:name")
    st := FromError(err)
    if len(st.Details) != 1 || st.Details[0] != "field:name" {
        t.Fatalf("details = %#v", st.Details)
    }
}
```

- [ ] **Step 12: 运行 status 测试确认失败**

Run: `rtk go test ./status`  
Expected: FAIL，错误包含 `undefined: Error`。

- [ ] **Step 13: 实现 status**

Create `status/code.go`:

```go
package status

type Code int

const (
    OK Code = iota
    Canceled
    Unknown
    InvalidArgument
    DeadlineExceeded
    NotFound
    AlreadyExists
    PermissionDenied
    ResourceExhausted
    FailedPrecondition
    Aborted
    OutOfRange
    Unimplemented
    Internal
    Unavailable
    DataLoss
    Unauthenticated
)
```

Create `status/status.go`:

```go
package status

type Status struct {
    Code    Code
    Message string
    Details []string
}

type rpcError struct {
    status Status
}

func (e *rpcError) Error() string {
    return e.status.Message
}

func Error(code Code, message string) error {
    return &rpcError{status: Status{Code: code, Message: message}}
}

func FromError(err error) Status {
    if err == nil {
        return Status{Code: OK}
    }
    if e, ok := err.(*rpcError); ok {
        return e.status
    }
    return Status{Code: Unknown, Message: err.Error()}
}

func WithDetails(err error, details ...string) error {
    st := FromError(err)
    copied := make([]string, len(details))
    copy(copied, details)
    st.Details = copied
    return &rpcError{status: st}
}
```

- [ ] **Step 14: 运行基础包测试确认通过**

Run: `rtk go test ./codec ./metadata ./status`  
Expected: PASS。

- [ ] **Step 15: 提交基础包**

Run:

```bash
rtk git add go.mod codec metadata status
rtk git commit -m "feat: add rpc codec metadata and status"
```

Expected: commit 成功。

## Task 2: protocol frame、session 状态机和流控

**Files:**
- Create: `protocol/frame.go`
- Create: `protocol/frame_test.go`
- Create: `protocol/session.go`
- Create: `protocol/session_test.go`
- Create: `protocol/window.go`
- Create: `protocol/window_test.go`

- [ ] **Step 1: 写 frame 测试**

Create `protocol/frame_test.go`:

```go
package protocol

import (
    "testing"

    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/status"
)

func TestFrameValidateRequest(t *testing.T) {
    frame := Frame{
        Type:     FrameRequest,
        StreamID: "s1",
        Metadata: metadata.MD{"method": []string{"user.Get"}},
        Payload:  []byte("body"),
    }
    if err := frame.Validate(); err != nil {
        t.Fatalf("Validate() error = %v", err)
    }
}

func TestFrameValidateRequiresStreamID(t *testing.T) {
    frame := Frame{Type: FrameData, Payload: []byte("body")}
    if err := frame.Validate(); err == nil {
        t.Fatalf("Validate() expected error")
    }
}

func TestStatusFrame(t *testing.T) {
    frame := Frame{
        Type:     FrameReset,
        StreamID: "s1",
        Status:   &status.Status{Code: status.Unavailable, Message: "closed"},
    }
    if err := frame.Validate(); err != nil {
        t.Fatalf("Validate() error = %v", err)
    }
}
```

- [ ] **Step 2: 运行 frame 测试确认失败**

Run: `rtk go test ./protocol -run TestFrame`  
Expected: FAIL，错误包含 `undefined: Frame`。

- [ ] **Step 3: 实现 frame 类型**

Create `protocol/frame.go`:

```go
package protocol

import (
    "errors"

    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/status"
)

type FrameType uint8

const (
    FrameRequest FrameType = iota + 1
    FrameResponse
    FrameData
    FrameWindowUpdate
    FrameEnd
    FrameReset
    FramePing
    FrameGoAway
)

type Direction uint8

const (
    DirectionNone Direction = iota
    DirectionClientToServer
    DirectionServerToClient
)

type Frame struct {
    Type      FrameType
    StreamID  string
    Seq       uint64
    Direction Direction
    Metadata  metadata.MD
    Payload   []byte
    Window    int
    Status    *status.Status
}

func (f Frame) Validate() error {
    if f.Type == 0 {
        return errors.New("protocol: frame type is required")
    }
    if f.Type != FramePing && f.Type != FrameGoAway && f.StreamID == "" {
        return errors.New("protocol: stream id is required")
    }
    return nil
}
```

- [ ] **Step 4: 运行 frame 测试确认通过**

Run: `rtk go test ./protocol -run TestFrame`  
Expected: PASS。

- [ ] **Step 5: 写 window 测试**

Create `protocol/window_test.go`:

```go
package protocol

import (
    "context"
    "testing"
    "time"
)

func TestWindowAcquireRelease(t *testing.T) {
    w := NewWindow(10)
    if err := w.Acquire(context.Background(), 6); err != nil {
        t.Fatalf("Acquire() error = %v", err)
    }
    w.Release(4)
    if got := w.Available(); got != 8 {
        t.Fatalf("available = %d", got)
    }
}

func TestWindowAcquireBlocksUntilRelease(t *testing.T) {
    w := NewWindow(5)
    if err := w.Acquire(context.Background(), 5); err != nil {
        t.Fatalf("Acquire() error = %v", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    done := make(chan error, 1)
    go func() {
        done <- w.Acquire(ctx, 3)
    }()
    time.Sleep(10 * time.Millisecond)
    w.Release(3)
    if err := <-done; err != nil {
        t.Fatalf("Acquire() after release error = %v", err)
    }
}

func TestWindowAcquireContextCanceled(t *testing.T) {
    w := NewWindow(1)
    if err := w.Acquire(context.Background(), 1); err != nil {
        t.Fatalf("Acquire() error = %v", err)
    }
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    if err := w.Acquire(ctx, 1); err == nil {
        t.Fatalf("Acquire() expected context error")
    }
}
```

- [ ] **Step 6: 运行 window 测试确认失败**

Run: `rtk go test ./protocol -run TestWindow`  
Expected: FAIL，错误包含 `undefined: NewWindow`。

- [ ] **Step 7: 实现 Window**

Create `protocol/window.go`:

```go
package protocol

import (
    "context"
    "sync"
)

type Window struct {
    mu        sync.Mutex
    changed   chan struct{}
    available int
}

func NewWindow(size int) *Window {
    return &Window{available: size, changed: make(chan struct{})}
}

func (w *Window) Available() int {
    w.mu.Lock()
    defer w.mu.Unlock()
    return w.available
}

func (w *Window) Acquire(ctx context.Context, n int) error {
    for {
        w.mu.Lock()
        if w.available >= n {
            w.available -= n
            w.mu.Unlock()
            return nil
        }
        changed := w.changed
        w.mu.Unlock()

        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-changed:
        }
    }
}

func (w *Window) Release(n int) {
    w.mu.Lock()
    w.available += n
    close(w.changed)
    w.changed = make(chan struct{})
    w.mu.Unlock()
}
```

- [ ] **Step 8: 写 session half-close 测试**

Create `protocol/session_test.go`:

```go
package protocol

import "testing"

func TestSessionHalfClose(t *testing.T) {
    s := NewSession("s1")
    if s.IsClosed() {
        t.Fatalf("new session is closed")
    }
    s.CloseSend()
    if s.IsClosed() {
        t.Fatalf("session closed before recv closed")
    }
    s.CloseRecv()
    if !s.IsClosed() {
        t.Fatalf("session should be closed")
    }
}

func TestSessionReset(t *testing.T) {
    s := NewSession("s1")
    s.Reset()
    if !s.IsReset() {
        t.Fatalf("session should be reset")
    }
    if !s.IsClosed() {
        t.Fatalf("reset session should be closed")
    }
}
```

- [ ] **Step 9: 运行 session 测试确认失败**

Run: `rtk go test ./protocol -run TestSession`  
Expected: FAIL，错误包含 `undefined: NewSession`。

- [ ] **Step 10: 实现 Session**

Create `protocol/session.go`:

```go
package protocol

import "sync"

type Session struct {
    id         string
    mu         sync.Mutex
    sendClosed bool
    recvClosed bool
    reset      bool
}

func NewSession(id string) *Session {
    return &Session{id: id}
}

func (s *Session) ID() string {
    return s.id
}

func (s *Session) CloseSend() {
    s.mu.Lock()
    s.sendClosed = true
    s.mu.Unlock()
}

func (s *Session) CloseRecv() {
    s.mu.Lock()
    s.recvClosed = true
    s.mu.Unlock()
}

func (s *Session) Reset() {
    s.mu.Lock()
    s.reset = true
    s.sendClosed = true
    s.recvClosed = true
    s.mu.Unlock()
}

func (s *Session) IsReset() bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.reset
}

func (s *Session) IsClosed() bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.sendClosed && s.recvClosed
}
```

- [ ] **Step 11: 运行 protocol 测试确认通过**

Run: `rtk go test ./protocol`  
Expected: PASS。

- [ ] **Step 12: 提交 protocol 基础**

Run:

```bash
rtk git add protocol
rtk git commit -m "feat: add rpc protocol frames and flow control primitives"
```

Expected: commit 成功。

## Task 3: transport 接口和 fake transport

**Files:**
- Create: `transport/types.go`
- Create: `transport/options.go`
- Create: `transport/fake/fake.go`
- Create: `transport/fake/fake_test.go`

- [ ] **Step 1: 写 fake transport 测试**

Create `transport/fake/fake_test.go`:

```go
package fake

import (
    "context"
    "testing"

    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/transport"
)

func TestFakeTransportOpenAcceptStream(t *testing.T) {
    tr := New()
    listener, err := tr.Listen(transport.Endpoint{Transport: "fake", Address: "svc"}, transport.ListenOptions{})
    if err != nil {
        t.Fatalf("Listen() error = %v", err)
    }
    clientConn, err := tr.Dial(context.Background(), transport.Endpoint{Transport: "fake", Address: "svc"}, transport.DialOptions{})
    if err != nil {
        t.Fatalf("Dial() error = %v", err)
    }
    serverConn, err := listener.Accept(context.Background())
    if err != nil {
        t.Fatalf("Accept() error = %v", err)
    }
    clientStream, err := clientConn.OpenStream(context.Background(), "user.Get", nil)
    if err != nil {
        t.Fatalf("OpenStream() error = %v", err)
    }
    serverStream, err := serverConn.AcceptStream(context.Background())
    if err != nil {
        t.Fatalf("AcceptStream() error = %v", err)
    }
    frame := &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID(), Payload: []byte("hello")}
    if err := clientStream.SendFrame(context.Background(), frame); err != nil {
        t.Fatalf("SendFrame() error = %v", err)
    }
    got, err := serverStream.RecvFrame(context.Background())
    if err != nil {
        t.Fatalf("RecvFrame() error = %v", err)
    }
    if string(got.Payload) != "hello" {
        t.Fatalf("payload = %q", got.Payload)
    }
}
```

- [ ] **Step 2: 运行 fake transport 测试确认失败**

Run: `rtk go test ./transport/fake`  
Expected: FAIL，错误包含 `undefined: New`。

- [ ] **Step 3: 定义 transport 接口**

Create `transport/types.go`:

```go
package transport

import (
    "context"

    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/status"
)

type Endpoint struct {
    Transport string
    Address   string
}

type Transport interface {
    Dial(ctx context.Context, endpoint Endpoint, opts DialOptions) (Conn, error)
    Listen(endpoint Endpoint, opts ListenOptions) (Listener, error)
    Name() string
}

type Listener interface {
    Accept(ctx context.Context) (Conn, error)
    Close(ctx context.Context) error
}

type Conn interface {
    ID() string
    LocalEndpoint() Endpoint
    RemoteEndpoint() Endpoint
    OpenStream(ctx context.Context, method string, md metadata.MD) (TransportStream, error)
    AcceptStream(ctx context.Context) (TransportStream, error)
    Close(ctx context.Context) error
    Drain(ctx context.Context) error
}

type TransportStream interface {
    ID() string
    SendFrame(ctx context.Context, frame *protocol.Frame) error
    RecvFrame(ctx context.Context) (*protocol.Frame, error)
    Close(ctx context.Context) error
    Reset(ctx context.Context, st *status.Status) error
}
```

Create `transport/options.go`:

```go
package transport

type DialOptions struct{}

type ListenOptions struct{}
```

- [ ] **Step 4: 实现 fake transport**

Create `transport/fake/fake.go`:

```go
package fake

import (
    "context"
    "errors"
    "sync"

    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/status"
    "github.com/hunyxv/zrpc/transport"
)

type Transport struct {
    mu        sync.Mutex
    listeners map[string]*listener
}

func New() *Transport {
    return &Transport{listeners: map[string]*listener{}}
}

func (t *Transport) Name() string { return "fake" }

func (t *Transport) Listen(endpoint transport.Endpoint, opts transport.ListenOptions) (transport.Listener, error) {
    t.mu.Lock()
    defer t.mu.Unlock()
    l := &listener{endpoint: endpoint, accepts: make(chan transport.Conn, 16)}
    t.listeners[endpoint.Address] = l
    return l, nil
}

func (t *Transport) Dial(ctx context.Context, endpoint transport.Endpoint, opts transport.DialOptions) (transport.Conn, error) {
    t.mu.Lock()
    l := t.listeners[endpoint.Address]
    t.mu.Unlock()
    if l == nil {
        return nil, errors.New("fake: listener not found")
    }
    client := newConn("client", endpoint)
    server := newConn("server", endpoint)
    client.peer = server
    server.peer = client
    select {
    case l.accepts <- server:
        return client, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

type listener struct {
    endpoint transport.Endpoint
    accepts  chan transport.Conn
}

func (l *listener) Accept(ctx context.Context) (transport.Conn, error) {
    select {
    case conn := <-l.accepts:
        return conn, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (l *listener) Close(ctx context.Context) error {
    close(l.accepts)
    return nil
}

type conn struct {
    id       string
    endpoint transport.Endpoint
    peer     *conn
    streams  chan transport.TransportStream
}

func newConn(id string, endpoint transport.Endpoint) *conn {
    return &conn{id: id, endpoint: endpoint, streams: make(chan transport.TransportStream, 16)}
}

func (c *conn) ID() string { return c.id }
func (c *conn) LocalEndpoint() transport.Endpoint { return c.endpoint }
func (c *conn) RemoteEndpoint() transport.Endpoint { return c.endpoint }
func (c *conn) Close(ctx context.Context) error { return nil }
func (c *conn) Drain(ctx context.Context) error { return nil }

func (c *conn) OpenStream(ctx context.Context, method string, md metadata.MD) (transport.TransportStream, error) {
    left := newStream("s1")
    right := newStream("s1")
    left.peer = right
    right.peer = left
    select {
    case c.peer.streams <- right:
        return left, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (c *conn) AcceptStream(ctx context.Context) (transport.TransportStream, error) {
    select {
    case stream := <-c.streams:
        return stream, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

type stream struct {
    id     string
    frames chan *protocol.Frame
    peer   *stream
}

func newStream(id string) *stream {
    return &stream{id: id, frames: make(chan *protocol.Frame, 16)}
}

func (s *stream) ID() string { return s.id }

func (s *stream) SendFrame(ctx context.Context, frame *protocol.Frame) error {
    select {
    case s.peer.frames <- frame:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (s *stream) RecvFrame(ctx context.Context) (*protocol.Frame, error) {
    select {
    case frame := <-s.frames:
        return frame, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (s *stream) Close(ctx context.Context) error { return nil }

func (s *stream) Reset(ctx context.Context, st *status.Status) error {
    return s.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameReset, StreamID: s.id, Status: st})
}
```

- [ ] **Step 5: 运行 fake transport 测试确认通过**

Run: `rtk go test ./transport/...`  
Expected: PASS。

- [ ] **Step 6: 提交 transport 接口和 fake transport**

Run:

```bash
rtk git add transport
rtk git commit -m "feat: add transport interfaces and fake transport"
```

Expected: commit 成功。

## Task 4: Request/Response、handler、interceptor、resolver、balancer

**Files:**
- Create: `request.go`
- Create: `request_test.go`
- Create: `handler.go`
- Create: `interceptor/interceptor.go`
- Create: `interceptor/interceptor_test.go`
- Create: `resolver/resolver.go`
- Create: `resolver/resolver_test.go`
- Create: `balancer/balancer.go`
- Create: `balancer/balancer_test.go`

- [ ] **Step 1: 写 Request/Response 测试**

Create `request_test.go`:

```go
package zrpc

import (
    "testing"

    "github.com/hunyxv/zrpc/codec"
)

type requestPayload struct {
    Name string `msgpack:"name" json:"name"`
}

func TestRequestDecodeAndResponseDecode(t *testing.T) {
    c := codec.Msgpack()
    req, err := NewRequest("user.Get", requestPayload{Name: "alice"}, c)
    if err != nil {
        t.Fatalf("NewRequest() error = %v", err)
    }
    var in requestPayload
    if err := req.Decode(&in); err != nil {
        t.Fatalf("Request.Decode() error = %v", err)
    }
    if in.Name != "alice" {
        t.Fatalf("request name = %q", in.Name)
    }
    resp, err := NewResponse(requestPayload{Name: "bob"}, c)
    if err != nil {
        t.Fatalf("NewResponse() error = %v", err)
    }
    var out requestPayload
    if err := resp.Decode(&out); err != nil {
        t.Fatalf("Response.Decode() error = %v", err)
    }
    if out.Name != "bob" {
        t.Fatalf("response name = %q", out.Name)
    }
}
```

- [ ] **Step 2: 运行 Request/Response 测试确认失败**

Run: `rtk go test . -run TestRequestDecodeAndResponseDecode`  
Expected: FAIL，错误包含 `undefined: NewRequest`。

- [ ] **Step 3: 实现 Request/Response 和 handler**

Create `request.go`:

```go
package zrpc

import (
    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/metadata"
)

type Request struct {
    Method   string
    Metadata metadata.MD
    Body     []byte
    Codec    codec.Codec
}

type Response struct {
    Metadata metadata.MD
    Body     []byte
    Codec    codec.Codec
}

func NewRequest(method string, value any, c codec.Codec) (*Request, error) {
    body, err := c.Marshal(value)
    if err != nil {
        return nil, err
    }
    return &Request{Method: method, Metadata: metadata.New(), Body: body, Codec: c}, nil
}

func (r *Request) Decode(v any) error {
    return r.Codec.Unmarshal(r.Body, v)
}

func NewResponse(value any, c codec.Codec) (*Response, error) {
    body, err := c.Marshal(value)
    if err != nil {
        return nil, err
    }
    return &Response{Metadata: metadata.New(), Body: body, Codec: c}, nil
}

func (r *Response) Decode(v any) error {
    return r.Codec.Unmarshal(r.Body, v)
}
```

Create `handler.go`:

```go
package zrpc

import "context"

type UnaryHandler interface {
    HandleUnary(ctx context.Context, req *Request) (*Response, error)
}

type UnaryHandlerFunc func(ctx context.Context, req *Request) (*Response, error)

func (f UnaryHandlerFunc) HandleUnary(ctx context.Context, req *Request) (*Response, error) {
    return f(ctx, req)
}

type StreamHandler interface {
    HandleStream(ctx context.Context, stream Stream) error
}

type StreamHandlerFunc func(ctx context.Context, stream Stream) error

func (f StreamHandlerFunc) HandleStream(ctx context.Context, stream Stream) error {
    return f(ctx, stream)
}
```

Create `stream.go` with initial interface only:

```go
package zrpc

import (
    "context"

    "github.com/hunyxv/zrpc/metadata"
)

type Stream interface {
    Context() context.Context
    Method() string
    Metadata() metadata.MD
    Send(ctx context.Context, msg any) error
    Recv(ctx context.Context, msg any) error
    CloseSend(ctx context.Context) error
    CloseRecv(ctx context.Context) error
    Reset(ctx context.Context, err error) error
}
```

- [ ] **Step 4: 运行 Request/Response 测试确认通过**

Run: `rtk go test . -run TestRequestDecodeAndResponseDecode`  
Expected: PASS。

- [ ] **Step 5: 写 interceptor 测试**

Create `interceptor/interceptor_test.go`:

```go
package interceptor

import (
    "context"
    "reflect"
    "testing"

    "github.com/hunyxv/zrpc"
)

func TestUnaryChainOrder(t *testing.T) {
    calls := []string{}
    first := func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error) {
        calls = append(calls, "first-before")
        resp, err := next.HandleUnary(ctx, req)
        calls = append(calls, "first-after")
        return resp, err
    }
    second := func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error) {
        calls = append(calls, "second-before")
        resp, err := next.HandleUnary(ctx, req)
        calls = append(calls, "second-after")
        return resp, err
    }
    final := zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
        calls = append(calls, "handler")
        return &zrpc.Response{}, nil
    })
    _, err := ChainUnary(first, second)(final).HandleUnary(context.Background(), &zrpc.Request{})
    if err != nil {
        t.Fatalf("HandleUnary() error = %v", err)
    }
    want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
    if !reflect.DeepEqual(calls, want) {
        t.Fatalf("calls = %#v want %#v", calls, want)
    }
}
```

- [ ] **Step 6: 实现 interceptor chain**

Create `interceptor/interceptor.go`:

```go
package interceptor

import (
    "context"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/protocol"
)

type UnaryInterceptor func(ctx context.Context, req *zrpc.Request, next zrpc.UnaryHandler) (*zrpc.Response, error)

type StreamInterceptor func(ctx context.Context, stream zrpc.Stream, next zrpc.StreamHandler) error

type MessageHook interface {
    OnSend(ctx context.Context, frame *protocol.Frame) error
    OnRecv(ctx context.Context, frame *protocol.Frame) error
}

func ChainUnary(items ...UnaryInterceptor) func(zrpc.UnaryHandler) zrpc.UnaryHandler {
    return func(final zrpc.UnaryHandler) zrpc.UnaryHandler {
        handler := final
        for i := len(items) - 1; i >= 0; i-- {
            current := items[i]
            next := handler
            handler = zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
                return current(ctx, req, next)
            })
        }
        return handler
    }
}
```

- [ ] **Step 7: 写 resolver/balancer 测试并实现**

Create `resolver/resolver_test.go`:

```go
package resolver

import (
    "context"
    "testing"

    "github.com/hunyxv/zrpc/transport"
)

func TestStaticResolverResolve(t *testing.T) {
    endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
    r := Static(endpoint)
    got, err := r.Resolve(context.Background(), "svc")
    if err != nil {
        t.Fatalf("Resolve() error = %v", err)
    }
    if len(got) != 1 || got[0] != endpoint {
        t.Fatalf("endpoints = %#v", got)
    }
}
```

Create `resolver/resolver.go`:

```go
package resolver

import (
    "context"

    "github.com/hunyxv/zrpc/transport"
)

type Update struct {
    Endpoints []transport.Endpoint
}

type Resolver interface {
    Resolve(ctx context.Context, target string) ([]transport.Endpoint, error)
    Watch(ctx context.Context, target string) (<-chan Update, error)
}

type staticResolver struct {
    endpoint transport.Endpoint
}

func Static(endpoint transport.Endpoint) Resolver {
    return staticResolver{endpoint: endpoint}
}

func (r staticResolver) Resolve(ctx context.Context, target string) ([]transport.Endpoint, error) {
    return []transport.Endpoint{r.endpoint}, nil
}

func (r staticResolver) Watch(ctx context.Context, target string) (<-chan Update, error) {
    ch := make(chan Update, 1)
    ch <- Update{Endpoints: []transport.Endpoint{r.endpoint}}
    close(ch)
    return ch, nil
}
```

Create `balancer/balancer_test.go`:

```go
package balancer

import (
    "context"
    "testing"

    "github.com/hunyxv/zrpc/transport"
)

func TestPickFirst(t *testing.T) {
    b := PickFirst()
    endpoints := []transport.Endpoint{
        {Transport: "fake", Address: "one"},
        {Transport: "fake", Address: "two"},
    }
    got, err := b.Pick(context.Background(), endpoints)
    if err != nil {
        t.Fatalf("Pick() error = %v", err)
    }
    if got != endpoints[0] {
        t.Fatalf("endpoint = %#v", got)
    }
}
```

Create `balancer/balancer.go`:

```go
package balancer

import (
    "context"
    "errors"

    "github.com/hunyxv/zrpc/transport"
)

type Balancer interface {
    Pick(ctx context.Context, endpoints []transport.Endpoint) (transport.Endpoint, error)
}

type pickFirst struct{}

func PickFirst() Balancer {
    return pickFirst{}
}

func (pickFirst) Pick(ctx context.Context, endpoints []transport.Endpoint) (transport.Endpoint, error) {
    if len(endpoints) == 0 {
        return transport.Endpoint{}, errors.New("balancer: no endpoints")
    }
    return endpoints[0], nil
}
```

- [ ] **Step 8: 运行基础核心测试**

Run: `rtk go test ./...`  
Expected: PASS。

- [ ] **Step 9: 提交 request/interceptor/resolver/balancer**

Run:

```bash
rtk git add request.go handler.go stream.go interceptor resolver balancer
rtk git commit -m "feat: add core request handlers and extension seams"
```

Expected: commit 成功。

## Task 5: Server/Client unary 核心，基于 fake transport

**Files:**
- Create: `server/options.go`
- Create: `server/server.go`
- Create: `server/server_test.go`
- Create: `client/options.go`
- Create: `client/client.go`
- Create: `client/client_test.go`
- Modify: `protocol/frame.go`

- [ ] **Step 1: 写 unary fake transport 集成测试**

Create `client/client_test.go`:

```go
package client

import (
    "context"
    "testing"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    "github.com/hunyxv/zrpc/transport/fake"
)

type unaryReq struct {
    Name string `msgpack:"name"`
}

type unaryResp struct {
    Message string `msgpack:"message"`
}

func TestClientInvokeUnary(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "svc"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
    srv.HandleUnary("hello.Say", zrpc.UnaryHandlerFunc(func(ctx context.Context, req *zrpc.Request) (*zrpc.Response, error) {
        var in unaryReq
        if err := req.Decode(&in); err != nil {
            t.Fatalf("Decode() error = %v", err)
        }
        return zrpc.NewResponse(unaryResp{Message: "hello " + in.Name}, codec.Msgpack())
    }))
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := New(Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    resp, err := cli.Invoke(context.Background(), "hello.Say", unaryReq{Name: "zrpc"})
    if err != nil {
        t.Fatalf("Invoke() error = %v", err)
    }
    var out unaryResp
    if err := resp.Decode(&out); err != nil {
        t.Fatalf("Decode() error = %v", err)
    }
    if out.Message != "hello zrpc" {
        t.Fatalf("message = %q", out.Message)
    }
}
```

- [ ] **Step 2: 运行 unary 集成测试确认失败**

Run: `rtk go test ./client -run TestClientInvokeUnary`  
Expected: FAIL，错误包含 `undefined: Options` 或 `undefined: New`。

- [ ] **Step 3: 定义 server options 和 client options**

Create `server/options.go`:

```go
package server

import (
    "time"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/transport"
)

type Options struct {
    Transport transport.Transport
    Endpoint  transport.Endpoint
    Codec     codec.Codec

    MaxConcurrentStreams      int
    MaxMessageSize            int
    InitialStreamWindow       int
    MaxConnInFlightBytes      int
    GracefulShutdownTimeout   time.Duration
}
```

Create `client/options.go`:

```go
package client

import (
    "time"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/transport"
)

type Options struct {
    Transport transport.Transport
    Target    transport.Endpoint
    Codec     codec.Codec

    DefaultTimeout       time.Duration
    MaxMessageSize       int
    InitialStreamWindow  int
    MaxConnInFlightBytes int
}
```

- [ ] **Step 4: 实现 server unary 核心**

Create `server/server.go`:

```go
package server

import (
    "context"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/status"
    "github.com/hunyxv/zrpc/transport"
)

type Server struct {
    opts   Options
    unary  map[string]zrpc.UnaryHandler
}

func New(opts Options) *Server {
    return &Server{opts: opts, unary: map[string]zrpc.UnaryHandler{}}
}

func (s *Server) HandleUnary(method string, handler zrpc.UnaryHandler) {
    s.unary[method] = handler
}

func (s *Server) Serve(ctx context.Context) error {
    listener, err := s.opts.Transport.Listen(s.opts.Endpoint, transport.ListenOptions{})
    if err != nil {
        return err
    }
    conn, err := listener.Accept(ctx)
    if err != nil {
        return err
    }
    for {
        stream, err := conn.AcceptStream(ctx)
        if err != nil {
            return err
        }
        go s.serveStream(ctx, stream)
    }
}

func (s *Server) serveStream(ctx context.Context, stream transport.TransportStream) {
    frame, err := stream.RecvFrame(ctx)
    if err != nil {
        return
    }
    method := frame.Metadata.Get("method")
    handler := s.unary[method]
    if handler == nil {
        _ = stream.SendFrame(ctx, &protocol.Frame{
            Type:     protocol.FrameResponse,
            StreamID: frame.StreamID,
            Status:   &status.Status{Code: status.Unimplemented, Message: "unknown method"},
        })
        return
    }
    req := &zrpc.Request{Method: method, Metadata: frame.Metadata, Body: frame.Payload, Codec: s.opts.Codec}
    resp, err := handler.HandleUnary(ctx, req)
    st := status.FromError(err)
    out := &protocol.Frame{Type: protocol.FrameResponse, StreamID: frame.StreamID, Status: &st}
    if resp != nil {
        out.Payload = resp.Body
        out.Metadata = resp.Metadata
    }
    _ = stream.SendFrame(ctx, out)
}
```

- [ ] **Step 5: 实现 client unary 核心**

Create `client/client.go`:

```go
package client

import (
    "context"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/status"
    "github.com/hunyxv/zrpc/transport"
)

type Client struct {
    opts Options
    conn transport.Conn
}

func New(opts Options) (*Client, error) {
    conn, err := opts.Transport.Dial(context.Background(), opts.Target, transport.DialOptions{})
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
    md := metadata.New()
    md.Set("method", method)
    stream, err := c.conn.OpenStream(ctx, method, md)
    if err != nil {
        return nil, err
    }
    frame := &protocol.Frame{Type: protocol.FrameRequest, StreamID: stream.ID(), Metadata: md, Payload: req.Body}
    if err := stream.SendFrame(ctx, frame); err != nil {
        return nil, err
    }
    respFrame, err := stream.RecvFrame(ctx)
    if err != nil {
        return nil, err
    }
    st := status.Status{Code: status.OK}
    if respFrame.Status != nil {
        st = *respFrame.Status
    }
    if st.Code != status.OK {
        return nil, status.Error(st.Code, st.Message)
    }
    return &zrpc.Response{Metadata: respFrame.Metadata, Body: respFrame.Payload, Codec: c.opts.Codec}, nil
}

func (c *Client) Close(ctx context.Context) error {
    return c.conn.Close(ctx)
}
```

- [ ] **Step 6: 运行 unary fake 集成测试**

Run: `rtk go test ./client ./server`  
Expected: PASS。

- [ ] **Step 7: 提交 unary 核心**

Run:

```bash
rtk git add client server request.go handler.go stream.go
rtk git commit -m "feat: add unary client server core"
```

Expected: commit 成功。

## Task 6: stream 核心和内部 backpressure

**Files:**
- Modify: `stream.go`
- Create: `stream_internal.go`
- Create: `stream_test.go`
- Modify: `client/client.go`
- Modify: `server/server.go`

- [ ] **Step 1: 写 client streaming 测试**

Create or extend `stream_test.go`:

```go
package zrpc_test

import (
    "context"
    "io"
    "testing"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    "github.com/hunyxv/zrpc/transport/fake"
)

type streamChunk struct {
    Value string `msgpack:"value"`
}

type streamResult struct {
    Count int `msgpack:"count"`
}

func TestClientStreaming(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "stream"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    srv.HandleStream("upload.Count", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
        count := 0
        for {
            var chunk streamChunk
            err := stream.Recv(ctx, &chunk)
            if err == io.EOF {
                return stream.Send(ctx, streamResult{Count: count})
            }
            if err != nil {
                return err
            }
            count++
        }
    }))
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    stream, err := cli.NewStream(context.Background(), "upload.Count")
    if err != nil {
        t.Fatalf("NewStream() error = %v", err)
    }
    if err := stream.Send(context.Background(), streamChunk{Value: "a"}); err != nil {
        t.Fatalf("Send() error = %v", err)
    }
    if err := stream.Send(context.Background(), streamChunk{Value: "b"}); err != nil {
        t.Fatalf("Send() error = %v", err)
    }
    if err := stream.CloseSend(context.Background()); err != nil {
        t.Fatalf("CloseSend() error = %v", err)
    }
    var result streamResult
    if err := stream.Recv(context.Background(), &result); err != nil {
        t.Fatalf("Recv() error = %v", err)
    }
    if result.Count != 2 {
        t.Fatalf("count = %d", result.Count)
    }
}
```

- [ ] **Step 2: 运行 stream 测试确认失败**

Run: `rtk go test . -run TestClientStreaming`  
Expected: FAIL，错误包含 `cli.NewStream undefined`。

- [ ] **Step 3: 实现 stream 内部结构**

Create `stream_internal.go`:

```go
package zrpc

import (
    "context"
    "io"
    "sync"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/metadata"
    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/status"
    "github.com/hunyxv/zrpc/transport"
)

type rpcStream struct {
    ctx      context.Context
    method   string
    md       metadata.MD
    codec    codec.Codec
    ts       transport.TransportStream
    sendWin  *protocol.Window
    recvWin  *protocol.Window
    mu       sync.Mutex
    sendDone bool
    recvDone bool
}

func newRPCStream(ctx context.Context, method string, md metadata.MD, c codec.Codec, ts transport.TransportStream, window int) *rpcStream {
    return &rpcStream{
        ctx:     ctx,
        method:  method,
        md:      md,
        codec:   c,
        ts:      ts,
        sendWin: protocol.NewWindow(window),
        recvWin: protocol.NewWindow(window),
    }
}

func (s *rpcStream) Context() context.Context { return s.ctx }
func (s *rpcStream) Method() string { return s.method }
func (s *rpcStream) Metadata() metadata.MD { return s.md }

func (s *rpcStream) Send(ctx context.Context, msg any) error {
    raw, err := s.codec.Marshal(msg)
    if err != nil {
        return err
    }
    if err := s.sendWin.Acquire(ctx, len(raw)); err != nil {
        return err
    }
    return s.ts.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameData, StreamID: s.ts.ID(), Payload: raw})
}

func (s *rpcStream) Recv(ctx context.Context, msg any) error {
    frame, err := s.ts.RecvFrame(ctx)
    if err != nil {
        return err
    }
    switch frame.Type {
    case protocol.FrameData:
        s.recvWin.Release(len(frame.Payload))
        return s.codec.Unmarshal(frame.Payload, msg)
    case protocol.FrameEnd:
        return io.EOF
    case protocol.FrameReset:
        if frame.Status != nil {
            return status.Error(frame.Status.Code, frame.Status.Message)
        }
        return status.Error(status.Unknown, "stream reset")
    default:
        return status.Error(status.Internal, "unexpected stream frame")
    }
}

func (s *rpcStream) CloseSend(ctx context.Context) error {
    s.mu.Lock()
    if s.sendDone {
        s.mu.Unlock()
        return nil
    }
    s.sendDone = true
    s.mu.Unlock()
    return s.ts.SendFrame(ctx, &protocol.Frame{Type: protocol.FrameEnd, StreamID: s.ts.ID()})
}

func (s *rpcStream) CloseRecv(ctx context.Context) error {
    s.mu.Lock()
    s.recvDone = true
    s.mu.Unlock()
    return nil
}

func (s *rpcStream) Reset(ctx context.Context, err error) error {
    st := status.FromError(err)
    return s.ts.Reset(ctx, &st)
}
```

- [ ] **Step 4: 接入 client.NewStream 和 server.HandleStream**

Modify `client/client.go` to add:

```go
func (c *Client) NewStream(ctx context.Context, method string) (zrpc.Stream, error) {
    md := metadata.New()
    md.Set("method", method)
    stream, err := c.conn.OpenStream(ctx, method, md)
    if err != nil {
        return nil, err
    }
    frame := &protocol.Frame{Type: protocol.FrameRequest, StreamID: stream.ID(), Metadata: md}
    if err := stream.SendFrame(ctx, frame); err != nil {
        return nil, err
    }
    return zrpc.NewInternalStream(ctx, method, md, c.opts.Codec, stream, c.opts.InitialStreamWindow), nil
}
```

Add exported constructor in root package:

```go
func NewInternalStream(ctx context.Context, method string, md metadata.MD, c codec.Codec, ts transport.TransportStream, window int) Stream {
    if window <= 0 {
        window = 1024 * 1024
    }
    return newRPCStream(ctx, method, md, c, ts, window)
}
```

Modify `server/server.go` to add stream registry and dispatch:

```go
type Server struct {
    opts   Options
    unary  map[string]zrpc.UnaryHandler
    stream map[string]zrpc.StreamHandler
}

func New(opts Options) *Server {
    return &Server{
        opts:   opts,
        unary:  map[string]zrpc.UnaryHandler{},
        stream: map[string]zrpc.StreamHandler{},
    }
}

func (s *Server) HandleStream(method string, handler zrpc.StreamHandler) {
    s.stream[method] = handler
}

func (s *Server) serveStream(ctx context.Context, stream transport.TransportStream) {
    frame, err := stream.RecvFrame(ctx)
    if err != nil {
        return
    }
    method := frame.Metadata.Get("method")
    if handler := s.stream[method]; handler != nil {
        rpcStream := zrpc.NewInternalStream(ctx, method, frame.Metadata, s.opts.Codec, stream, s.opts.InitialStreamWindow)
        if err := handler.HandleStream(ctx, rpcStream); err != nil {
            _ = rpcStream.Reset(ctx, err)
        }
        return
    }
    handler := s.unary[method]
    if handler == nil {
        _ = stream.SendFrame(ctx, &protocol.Frame{
            Type:     protocol.FrameResponse,
            StreamID: frame.StreamID,
            Status:   &status.Status{Code: status.Unimplemented, Message: "unknown method"},
        })
        return
    }
    req := &zrpc.Request{Method: method, Metadata: frame.Metadata, Body: frame.Payload, Codec: s.opts.Codec}
    resp, err := handler.HandleUnary(ctx, req)
    st := status.FromError(err)
    out := &protocol.Frame{Type: protocol.FrameResponse, StreamID: frame.StreamID, Status: &st}
    if resp != nil {
        out.Payload = resp.Body
        out.Metadata = resp.Metadata
    }
    _ = stream.SendFrame(ctx, out)
}
```

- [ ] **Step 5: 运行 stream 测试**

Run: `rtk go test ./... -run "TestClientStreaming|TestWindow|TestSession"`  
Expected: PASS。

- [ ] **Step 6: 增加 server streaming 和 bidi streaming 测试**

Extend `stream_test.go` with:

```go
func TestServerStreaming(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "stream-server"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    srv.HandleStream("download.List", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
        var req streamChunk
        if err := stream.Recv(ctx, &req); err != nil {
            return err
        }
        if err := stream.Send(ctx, streamChunk{Value: req.Value + "-1"}); err != nil {
            return err
        }
        if err := stream.Send(ctx, streamChunk{Value: req.Value + "-2"}); err != nil {
            return err
        }
        return stream.CloseSend(ctx)
    }))
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    stream, err := cli.NewStream(context.Background(), "download.List")
    if err != nil {
        t.Fatalf("NewStream() error = %v", err)
    }
    if err := stream.Send(context.Background(), streamChunk{Value: "item"}); err != nil {
        t.Fatalf("Send() error = %v", err)
    }
    if err := stream.CloseSend(context.Background()); err != nil {
        t.Fatalf("CloseSend() error = %v", err)
    }
    var first streamChunk
    if err := stream.Recv(context.Background(), &first); err != nil {
        t.Fatalf("Recv(first) error = %v", err)
    }
    var second streamChunk
    if err := stream.Recv(context.Background(), &second); err != nil {
        t.Fatalf("Recv(second) error = %v", err)
    }
    if first.Value != "item-1" || second.Value != "item-2" {
        t.Fatalf("chunks = %q, %q", first.Value, second.Value)
    }
    var end streamChunk
    if err := stream.Recv(context.Background(), &end); err != io.EOF {
        t.Fatalf("Recv(end) error = %v, want io.EOF", err)
    }
}

func TestBidiStreaming(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "stream-bidi"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    srv.HandleStream("chat.Echo", zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
        for {
            var in streamChunk
            err := stream.Recv(ctx, &in)
            if err == io.EOF {
                return stream.CloseSend(ctx)
            }
            if err != nil {
                return err
            }
            if err := stream.Send(ctx, streamChunk{Value: in.Value + "!"}); err != nil {
                return err
            }
        }
    }))
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    stream, err := cli.NewStream(context.Background(), "chat.Echo")
    if err != nil {
        t.Fatalf("NewStream() error = %v", err)
    }
    if err := stream.Send(context.Background(), streamChunk{Value: "a"}); err != nil {
        t.Fatalf("Send() error = %v", err)
    }
    var out streamChunk
    if err := stream.Recv(context.Background(), &out); err != nil {
        t.Fatalf("Recv() error = %v", err)
    }
    if out.Value != "a!" {
        t.Fatalf("value = %q", out.Value)
    }
    if err := stream.CloseSend(context.Background()); err != nil {
        t.Fatalf("CloseSend() error = %v", err)
    }
    var end streamChunk
    if err := stream.Recv(context.Background(), &end); err != io.EOF {
        t.Fatalf("Recv(end) error = %v, want io.EOF", err)
    }
}
```

- [ ] **Step 7: 运行全部 stream 测试**

Run: `rtk go test ./... -run "TestClientStreaming|TestServerStreaming|TestBidiStreaming"`  
Expected: PASS。

- [ ] **Step 8: 提交 stream 核心**

Run:

```bash
rtk git add stream.go stream_internal.go stream_test.go client server
rtk git commit -m "feat: add rpc streams with internal backpressure"
```

Expected: commit 成功。

## Task 7: metrics、trace、security middleware seam

**Files:**
- Create: `metrics/metrics.go`
- Create: `metrics/metrics_test.go`
- Create: `trace/trace.go`
- Create: `trace/trace_test.go`
- Create: `security/principal.go`
- Create: `security/principal_test.go`
- Modify: `client/client.go`
- Modify: `server/server.go`

- [ ] **Step 1: 写 security principal 测试**

Create `security/principal_test.go`:

```go
package security

import (
    "context"
    "testing"
)

func TestPrincipalContext(t *testing.T) {
    p := &Principal{Subject: "user-1", Claims: map[string]string{"tenant": "t1"}}
    ctx := ContextWithPrincipal(context.Background(), p)
    got, ok := PrincipalFromContext(ctx)
    if !ok {
        t.Fatalf("principal missing")
    }
    if got.Subject != "user-1" || got.Claims["tenant"] != "t1" {
        t.Fatalf("principal = %#v", got)
    }
}
```

- [ ] **Step 2: 实现 security principal**

Create `security/principal.go`:

```go
package security

import "context"

type Principal struct {
    Subject string
    Claims  map[string]string
}

type principalKey struct{}

func ContextWithPrincipal(ctx context.Context, p *Principal) context.Context {
    return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
    p, ok := ctx.Value(principalKey{}).(*Principal)
    return p, ok
}
```

- [ ] **Step 3: 写 metrics 测试并实现 noop collector**

Create `metrics/metrics_test.go`:

```go
package metrics

import (
    "context"
    "testing"
    "time"

    "github.com/hunyxv/zrpc/status"
)

func TestNoopCollector(t *testing.T) {
    c := Noop()
    c.OnRPCStart(context.Background(), RPCInfo{Method: "hello.Say"})
    c.OnRPCFinish(context.Background(), RPCInfo{Method: "hello.Say"}, &status.Status{Code: status.OK}, time.Millisecond)
    c.OnStreamEvent(context.Background(), StreamEvent{Method: "hello.Stream"})
    c.OnTransportEvent(context.Background(), TransportEvent{Transport: "fake"})
}
```

Create `metrics/metrics.go`:

```go
package metrics

import (
    "context"
    "time"

    "github.com/hunyxv/zrpc/status"
)

type RPCInfo struct {
    Method string
}

type StreamEvent struct {
    Method string
    Bytes  int
}

type TransportEvent struct {
    Transport string
    Error     error
}

type Collector interface {
    OnRPCStart(ctx context.Context, info RPCInfo)
    OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration)
    OnStreamEvent(ctx context.Context, event StreamEvent)
    OnTransportEvent(ctx context.Context, event TransportEvent)
}

type noopCollector struct{}

func Noop() Collector { return noopCollector{} }

func (noopCollector) OnRPCStart(ctx context.Context, info RPCInfo) {}
func (noopCollector) OnRPCFinish(ctx context.Context, info RPCInfo, st *status.Status, dur time.Duration) {}
func (noopCollector) OnStreamEvent(ctx context.Context, event StreamEvent) {}
func (noopCollector) OnTransportEvent(ctx context.Context, event TransportEvent) {}
```

- [ ] **Step 4: 写 trace 测试并实现 trace helper**

Create `trace/trace_test.go`:

```go
package trace

import (
    "context"
    "testing"

    "github.com/hunyxv/zrpc/metadata"
)

func TestInjectExtractNoop(t *testing.T) {
    md := metadata.New()
    Inject(context.Background(), md)
    ctx := Extract(context.Background(), md)
    if ctx == nil {
        t.Fatalf("Extract returned nil context")
    }
}
```

Create `trace/trace.go`:

```go
package trace

import (
    "context"

    "github.com/hunyxv/zrpc/metadata"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/propagation"
)

type carrier struct {
    md metadata.MD
}

func (c carrier) Get(key string) string { return c.md.Get(key) }
func (c carrier) Set(key, value string) { c.md.Set(key, value) }
func (c carrier) Keys() []string {
    keys := make([]string, 0, len(c.md))
    for key := range c.md {
        keys = append(keys, key)
    }
    return keys
}

func Inject(ctx context.Context, md metadata.MD) {
    otel.GetTextMapPropagator().Inject(ctx, carrier{md: md})
}

func Extract(ctx context.Context, md metadata.MD) context.Context {
    return otel.GetTextMapPropagator().Extract(ctx, carrier{md: md})
}

func Propagator() propagation.TextMapPropagator {
    return otel.GetTextMapPropagator()
}
```

- [ ] **Step 5: 将 metrics/trace 接入 client/server options**

Modify `client/options.go` and `server/options.go` to add:

```go
Metrics metrics.Collector
```

Set default to `metrics.Noop()` in `New` constructors when nil.

In `Client.Invoke`, call:

```go
c.opts.Metrics.OnRPCStart(ctx, metrics.RPCInfo{Method: method})
start := time.Now()
defer func() {
    st := status.FromError(err)
    c.opts.Metrics.OnRPCFinish(ctx, metrics.RPCInfo{Method: method}, &st, time.Since(start))
}()
```

Use named return values in `Invoke` to make the defer observe final `err`.

- [ ] **Step 6: 运行观测性测试**

Run: `rtk go test ./metrics ./trace ./security ./client ./server`  
Expected: PASS。

- [ ] **Step 7: 提交观测性扩展点**

Run:

```bash
rtk git add metrics trace security client server
rtk git commit -m "feat: add observability and security extension seams"
```

Expected: commit 成功。

## Task 8: typed helper API

**Files:**
- Create: `typed/unary.go`
- Create: `typed/stream.go`
- Create: `typed/typed_test.go`

- [ ] **Step 1: 写 typed unary helper 测试**

Create `typed/typed_test.go`:

```go
package typed

import (
    "context"
    "io"
    "strconv"
    "testing"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    "github.com/hunyxv/zrpc/transport/fake"
)

type helloReq struct {
    Name string `msgpack:"name"`
}

type helloResp struct {
    Message string `msgpack:"message"`
}

func TestTypedUnary(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "typed"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
    HandleUnary[helloReq, helloResp](srv, "hello.Say", func(ctx context.Context, req *helloReq) (*helloResp, error) {
        return &helloResp{Message: "hello " + req.Name}, nil
    })
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()
    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    resp, err := Invoke[helloReq, helloResp](context.Background(), cli, "hello.Say", &helloReq{Name: "typed"})
    if err != nil {
        t.Fatalf("Invoke() error = %v", err)
    }
    if resp.Message != "hello typed" {
        t.Fatalf("message = %q", resp.Message)
    }
}
```

- [ ] **Step 2: 实现 typed unary helper**

Create `typed/unary.go`:

```go
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
    var out Resp
    if err := rawResp.Decode(&out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

- [ ] **Step 3: 写 typed stream helper 测试**

Extend `typed/typed_test.go` with:

```go
func TestTypedClientStream(t *testing.T) {
    tr := fake.New()
    endpoint := transport.Endpoint{Transport: "fake", Address: "typed-stream"}
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    HandleClientStream[helloReq, helloResp](srv, "upload.Count", func(ctx context.Context, stream *ServerStream[helloReq, helloResp]) error {
        count := 0
        for {
            _, err := stream.Recv(ctx)
            if err == io.EOF {
                return stream.SendAndClose(ctx, &helloResp{Message: strconv.Itoa(count)})
            }
            if err != nil {
                return err
            }
            count++
        }
    })
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()
    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    stream, err := NewClientStream[helloReq, helloResp](context.Background(), cli, "upload.Count")
    if err != nil {
        t.Fatalf("NewClientStream() error = %v", err)
    }
    if err := stream.Send(context.Background(), &helloReq{Name: "a"}); err != nil {
        t.Fatalf("Send(a) error = %v", err)
    }
    if err := stream.Send(context.Background(), &helloReq{Name: "b"}); err != nil {
        t.Fatalf("Send(b) error = %v", err)
    }
    resp, err := stream.CloseAndRecv(context.Background())
    if err != nil {
        t.Fatalf("CloseAndRecv() error = %v", err)
    }
    if resp.Message != "2" {
        t.Fatalf("message = %q", resp.Message)
    }
}
```

- [ ] **Step 4: 实现 typed stream helper**

Create `typed/stream.go`:

```go
package typed

import (
    "context"

    "github.com/hunyxv/zrpc"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
)

type ClientStream[Req any, Resp any] struct {
    stream zrpc.Stream
}

type ServerStream[Req any, Resp any] struct {
    stream zrpc.Stream
}

func (s *ClientStream[Req, Resp]) Send(ctx context.Context, req *Req) error {
    return s.stream.Send(ctx, req)
}

func (s *ClientStream[Req, Resp]) CloseAndRecv(ctx context.Context) (*Resp, error) {
    if err := s.stream.CloseSend(ctx); err != nil {
        return nil, err
    }
    var out Resp
    if err := s.stream.Recv(ctx, &out); err != nil {
        return nil, err
    }
    return &out, nil
}

func NewClientStream[Req any, Resp any](ctx context.Context, cli *client.Client, method string) (*ClientStream[Req, Resp], error) {
    stream, err := cli.NewStream(ctx, method)
    if err != nil {
        return nil, err
    }
    return &ClientStream[Req, Resp]{stream: stream}, nil
}

func (s *ServerStream[Req, Resp]) Recv(ctx context.Context) (*Req, error) {
    var in Req
    if err := s.stream.Recv(ctx, &in); err != nil {
        return nil, err
    }
    return &in, nil
}

func (s *ServerStream[Req, Resp]) SendAndClose(ctx context.Context, resp *Resp) error {
    if err := s.stream.Send(ctx, resp); err != nil {
        return err
    }
    return s.stream.CloseSend(ctx)
}

func HandleClientStream[Req any, Resp any](srv *server.Server, method string, handler func(context.Context, *ServerStream[Req, Resp]) error) {
    srv.HandleStream(method, zrpc.StreamHandlerFunc(func(ctx context.Context, stream zrpc.Stream) error {
        return handler(ctx, &ServerStream[Req, Resp]{stream: stream})
    }))
}
```

- [ ] **Step 5: 运行 typed 测试**

Run: `rtk go test ./typed`  
Expected: PASS。

- [ ] **Step 6: 提交 typed helper**

Run:

```bash
rtk git add typed
rtk git commit -m "feat: add typed rpc helper APIs"
```

Expected: commit 成功。

## Task 9: ZeroMQ transport v1

**Files:**
- Create: `transport/zmq/options.go`
- Create: `transport/zmq/client.go`
- Create: `transport/zmq/server.go`
- Create: `transport/zmq/conn.go`
- Create: `transport/zmq/zmq_test.go`

- [ ] **Step 1: 写 ZeroMQ smoke 测试**

Create `transport/zmq/zmq_test.go`:

```go
package zmq

import (
    "context"
    "testing"
    "time"

    "github.com/hunyxv/zrpc/protocol"
    "github.com/hunyxv/zrpc/transport"
)

func TestZMQTransportFrameRoundTrip(t *testing.T) {
    endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19090"}
    tr := New(Options{SndHWM: 100, RcvHWM: 100, Linger: time.Millisecond * 100, RouterMandatory: true, Immediate: true})
    listener, err := tr.Listen(endpoint, transport.ListenOptions{})
    if err != nil {
        t.Fatalf("Listen() error = %v", err)
    }
    defer listener.Close(context.Background())

    serverConnCh := make(chan transport.Conn, 1)
    go func() {
        conn, err := listener.Accept(context.Background())
        if err == nil {
            serverConnCh <- conn
        }
    }()

    clientConn, err := tr.Dial(context.Background(), endpoint, transport.DialOptions{})
    if err != nil {
        t.Fatalf("Dial() error = %v", err)
    }
    serverConn := <-serverConnCh
    clientStream, err := clientConn.OpenStream(context.Background(), "hello.Say", nil)
    if err != nil {
        t.Fatalf("OpenStream() error = %v", err)
    }
    serverStream, err := serverConn.AcceptStream(context.Background())
    if err != nil {
        t.Fatalf("AcceptStream() error = %v", err)
    }
    frame := &protocol.Frame{Type: protocol.FrameData, StreamID: clientStream.ID(), Payload: []byte("hello")}
    if err := clientStream.SendFrame(context.Background(), frame); err != nil {
        t.Fatalf("SendFrame() error = %v", err)
    }
    got, err := serverStream.RecvFrame(context.Background())
    if err != nil {
        t.Fatalf("RecvFrame() error = %v", err)
    }
    if string(got.Payload) != "hello" {
        t.Fatalf("payload = %q", got.Payload)
    }
}
```

- [ ] **Step 2: 运行 ZeroMQ 测试确认失败**

Run: `rtk go test ./transport/zmq -run TestZMQTransportFrameRoundTrip`  
Expected: FAIL，错误包含 `undefined: New`。

- [ ] **Step 3: 实现 ZeroMQ options**

Create `transport/zmq/options.go`:

```go
package zmq

import "time"

type Options struct {
    SndHWM          int
    RcvHWM          int
    Linger          time.Duration
    Immediate       bool
    RouterMandatory bool
    SendQueueSize   int
    RecvQueueSize   int
}

func defaultOptions(opts Options) Options {
    if opts.SndHWM == 0 {
        opts.SndHWM = 1000
    }
    if opts.RcvHWM == 0 {
        opts.RcvHWM = 1000
    }
    if opts.Linger == 0 {
        opts.Linger = time.Second
    }
    if opts.SendQueueSize == 0 {
        opts.SendQueueSize = 1024
    }
    if opts.RecvQueueSize == 0 {
        opts.RecvQueueSize = 1024
    }
    return opts
}
```

- [ ] **Step 4: 实现 ZeroMQ transport owner goroutine**

Create `transport/zmq/client.go`, `server.go`, and `conn.go` with these required properties:

```go
package zmq

import (
    "context"

    "github.com/hunyxv/zrpc/transport"
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
```

Implementation constraints for `conn.go`:

- ZeroMQ socket must only be touched in one owner goroutine.
- Owner goroutine must receive send requests on a channel and send result back.
- Recv dispatcher must decode frames and route by `StreamID`.
- `ROUTER_MANDATORY`, finite HWM, finite Linger must be configured.
- No `time.Sleep` for lifecycle.
- `Close(ctx)` waits on done channel.

- [ ] **Step 5: 运行 ZeroMQ transport 测试**

Run: `rtk go test ./transport/zmq -run TestZMQTransportFrameRoundTrip`  
Expected: PASS。

- [ ] **Step 6: 运行全 transport 测试**

Run: `rtk go test ./transport/...`  
Expected: PASS。

- [ ] **Step 7: 提交 ZeroMQ transport**

Run:

```bash
rtk git add transport/zmq
rtk git commit -m "feat: add zeromq transport"
```

Expected: commit 成功。

## Task 10: 新 README、示例和端到端测试

**Files:**
- Replace: `README.md`
- Create: `_example/v1_unary/main.go`
- Create: `_example/v1_stream/main.go`
- Create: `testdata/v1_e2e_test.go`

- [ ] **Step 1: 写端到端测试**

Create `testdata/v1_e2e_test.go`:

```go
package testdata

import (
    "context"
    "testing"
    "time"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
    "github.com/hunyxv/zrpc/typed"
)

type e2eReq struct {
    Name string `msgpack:"name"`
}

type e2eResp struct {
    Message string `msgpack:"message"`
}

func TestV1UnaryE2E(t *testing.T) {
    endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19191"}
    tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second})
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
    typed.HandleUnary[e2eReq, e2eResp](srv, "hello.Say", func(ctx context.Context, req *e2eReq) (*e2eResp, error) {
        return &e2eResp{Message: "hello " + req.Name}, nil
    })
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
    if err != nil {
        t.Fatalf("client.New() error = %v", err)
    }
    resp, err := typed.Invoke[e2eReq, e2eResp](context.Background(), cli, "hello.Say", &e2eReq{Name: "zrpc"})
    if err != nil {
        t.Fatalf("Invoke() error = %v", err)
    }
    if resp.Message != "hello zrpc" {
        t.Fatalf("message = %q", resp.Message)
    }
}
```

- [ ] **Step 2: 运行端到端测试**

Run: `rtk go test ./testdata -run TestV1UnaryE2E`  
Expected: PASS。

- [ ] **Step 3: 重写 README**

Replace `README.md` with Chinese v1 documentation:

```markdown
# zrpc

zrpc 是一个 Go-to-Go RPC 框架，v1 使用显式 Handler/Client API，支持 unary、client streaming、server streaming 和 bidirectional streaming。

## v1 特性

- Transport 抽象，首个实现为 ZeroMQ。
- 默认 msgpack codec，内置 json 调试 codec。
- Unary 和 stream middleware。
- OpenTelemetry 链路追踪。
- 可插拔 metrics collector。
- RPC status code 错误模型。
- 内部 per-stream backpressure。
- v1 单节点运行，预留 Resolver/Balancer 扩展点。

## Unary 示例

```go
typed.HandleUnary[HelloReq, HelloResp](srv, "hello.Say", handler)
resp, err := typed.Invoke[HelloReq, HelloResp](ctx, cli, "hello.Say", &HelloReq{Name: "zrpc"})
```

## v1 不包含

- 集群
- etcd/consul/zk 服务发现
- 节点间转发
- 跨语言 SDK
- IDL/codegen
- 自动 retry
- ZeroMQ 传输层 TLS/mTLS
```

- [ ] **Step 4: 创建示例**

Create `_example/v1_unary/main.go`:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
    "github.com/hunyxv/zrpc/typed"
)

type HelloReq struct {
    Name string `msgpack:"name"`
}

type HelloResp struct {
    Message string `msgpack:"message"`
}

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19201"}
    tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second})
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack()})
    typed.HandleUnary[HelloReq, HelloResp](srv, "hello.Say", func(ctx context.Context, req *HelloReq) (*HelloResp, error) {
        return &HelloResp{Message: "hello " + req.Name}, nil
    })
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack()})
    if err != nil {
        panic(err)
    }
    resp, err := typed.Invoke[HelloReq, HelloResp](ctx, cli, "hello.Say", &HelloReq{Name: "zrpc"})
    if err != nil {
        panic(err)
    }
    fmt.Println(resp.Message)
}
```

Create `_example/v1_stream/main.go`:

```go
package main

import (
    "context"
    "fmt"
    "io"
    "strconv"
    "time"

    "github.com/hunyxv/zrpc/codec"
    "github.com/hunyxv/zrpc/client"
    "github.com/hunyxv/zrpc/server"
    "github.com/hunyxv/zrpc/transport"
    zrpczmq "github.com/hunyxv/zrpc/transport/zmq"
    "github.com/hunyxv/zrpc/typed"
)

type UploadReq struct {
    Name string `msgpack:"name"`
}

type UploadResp struct {
    Message string `msgpack:"message"`
}

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    endpoint := transport.Endpoint{Transport: "zmq", Address: "tcp://127.0.0.1:19202"}
    tr := zrpczmq.New(zrpczmq.Options{SndHWM: 100, RcvHWM: 100, Linger: time.Second})
    srv := server.New(server.Options{Transport: tr, Endpoint: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    typed.HandleClientStream[UploadReq, UploadResp](srv, "upload.Count", func(ctx context.Context, stream *typed.ServerStream[UploadReq, UploadResp]) error {
        count := 0
        for {
            _, err := stream.Recv(ctx)
            if err == io.EOF {
                return stream.SendAndClose(ctx, &UploadResp{Message: strconv.Itoa(count)})
            }
            if err != nil {
                return err
            }
            count++
        }
    })
    go func() {
        _ = srv.Serve(ctx)
    }()

    cli, err := client.New(client.Options{Transport: tr, Target: endpoint, Codec: codec.Msgpack(), InitialStreamWindow: 1024})
    if err != nil {
        panic(err)
    }
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
```

- [ ] **Step 5: 运行全部测试**

Run: `rtk go test ./...`  
Expected: PASS。

- [ ] **Step 6: 运行 race 测试**

Run: `rtk go test -race ./...`  
Expected: PASS。

- [ ] **Step 7: 提交文档和示例**

Run:

```bash
rtk git add README.md _example/v1_unary _example/v1_stream testdata/v1_e2e_test.go
rtk git commit -m "docs: add v1 rpc examples and README"
```

Expected: commit 成功。

## Task 11: 清理旧实现入口和最终验收

**Files:**
- Modify/Delete after confirmation: old `broker.go`, `methodfunc.go`, `multiplexer.go`, old `client/channel.go`, old `client/methodproxy.go`, old examples.
- Modify: `go.mod`
- Modify: `README.md`

- [ ] **Step 1: 列出旧 API 引用**

Run: `rtk rg -n "RegisterServer|Decorator|DefaultNode|NewDirectClient|broker|peerNodeManager|SvcMultiplexer"`  
Expected: 输出旧 API 引用列表。

- [ ] **Step 2: 决定旧 API 处理方式**

选择一种并执行：

- 删除旧文件：适合 v1 完全替换。
- 移到 `internal/legacy_disabled`：适合短期保留参考但不编译。

推荐删除旧文件，前提是新 e2e 和 examples 已通过。

- [ ] **Step 3: 删除旧实现或隔离旧实现**

If deleting:

```bash
rtk git rm broker.go methodfunc.go multiplexer.go zmq.go registry.go registry_etcd.go registry_consul.go registry_zk.go
rtk git rm client/channel.go client/methodproxy.go client/connect_manager.go client/pool.go
```

Expected: 删除成功。

- [ ] **Step 4: 整理 go.mod**

Run: `rtk go mod tidy`  
Expected: `go.mod` 和 `go.sum` 删除旧 registry/cluster 依赖，保留 msgpack、zmq4、OpenTelemetry 等 v1 必需依赖。

- [ ] **Step 5: 运行最终测试**

Run:

```bash
rtk go test ./...
rtk go test -race ./...
```

Expected: PASS。

- [ ] **Step 6: 检查工作区差异**

Run:

```bash
rtk git status --short
rtk git diff --stat
```

Expected: 只包含本任务产生的代码、测试、文档、go.mod/go.sum 变化。

- [ ] **Step 7: 提交最终清理**

Run:

```bash
rtk git add .
rtk git commit -m "refactor: replace legacy rpc stack with v1 core"
```

Expected: commit 成功。

## 覆盖性自检

- Spec 目标：四种 RPC 模式  
  覆盖任务：Task 6、Task 8、Task 10。

- Spec 目标：显式核心 API + 泛型 helper  
  覆盖任务：Task 5、Task 8。

- Spec 目标：Transport 抽象，ZeroMQ first  
  覆盖任务：Task 3、Task 9。

- Spec 目标：v1 不实现集群，但预留 Resolver/Balancer  
  覆盖任务：Task 4。

- Spec 目标：msgpack 默认、json 调试  
  覆盖任务：Task 1。

- Spec 目标：middleware、trace、metrics、security seam  
  覆盖任务：Task 4、Task 7。

- Spec 目标：status code + details  
  覆盖任务：Task 1、Task 5。

- Spec 目标：内部 per-stream backpressure  
  覆盖任务：Task 2、Task 6。

- Spec 目标：测试策略  
  覆盖任务：Task 1 到 Task 11 的单元测试、fake transport 测试、ZeroMQ 集成测试和 race 测试。

## 执行顺序建议

1. Task 1 到 Task 4 建立可测试核心基础。
2. Task 5 先打通 unary。
3. Task 6 再打通 stream 和 backpressure。
4. Task 7 接入 observability/security seam。
5. Task 8 加 typed helper。
6. Task 9 实现 ZeroMQ transport。
7. Task 10 加 README、示例和 e2e。
8. Task 11 再删除旧代码，避免中途失去参考和回滚点。

## 最终验收命令

```bash
rtk go test ./...
rtk go test -race ./...
rtk git status --short
```

最终状态要求：

- 所有测试通过。
- `go test -race ./...` 通过。
- README 展示新 API。
- examples 使用新 API。
- 旧 broker/cluster/proxy 核心不再参与编译。
- `.gitignore` 等用户未明确要求修改的文件不被误提交。
