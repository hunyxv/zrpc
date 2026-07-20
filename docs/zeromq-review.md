# ZeroMQ Transport v1 实现说明

本文描述当前 `transport/zmq` 的实际实现、运行边界和测试覆盖。此次协议新增连接控制帧，
不兼容旧 wire 格式，通信双方需要同时升级。

## 拓扑

- 服务端使用一个 ROUTER socket；每个 DEALER identity 映射为一条逻辑 `conn`。
- 客户端每条连接使用独占 DEALER socket 和 owner goroutine。
- ZeroMQ socket/context 只由 owner goroutine 操作；业务 goroutine 通过有界队列提交发送。
- 服务端逻辑 conn 共享 listener 的 ROUTER owner，关闭逻辑 conn 不关闭共享 socket。
- frame 使用 msgpack 编码，ROUTER multipart 的首段是 route，末段是协议 frame。

## 控制帧

连接级控制帧为 `Ping`、`Pong`、`GoAway`、`Close` 和 `CloseAck`。它们必须携带非零
`Seq`，且不得携带 StreamID、metadata、payload、window、status 或 direction。

- `GoAway(seq)`：通知对端进入 Drain，不再创建新 stream。
- `Close(seq)`：发起连接关闭。
- `CloseAck(seq)`：确认相同 Seq 的 Close。
- `Ping(seq)` / `Pong(seq)`：仅在连接空闲时探测存活。

`WindowUpdate` 和 `Reset` 是 stream 级控制帧，也进入高优先级 control queue。

## 状态机

每条 conn 分别记录 local 和 peer 的 `Active`、`Draining`、`Closing`、`Closed` 状态。

| 状态 | OpenStream | 新入站 Request | 已有 stream |
| --- | --- | --- | --- |
| Active | 允许 | 允许 | 正常 |
| 任一侧 Draining | 拒绝 | Reset(Unavailable) | 允许完成 |
| Closing | 拒绝 | 丢弃 | 本地终止 |
| Closed | closed error | 丢弃 | 已回收 |

`Drain(ctx)` 发送 GoAway。Drain 前已进入 incoming queue 的 stream 仍可被接受；Drain 后的
新 Request 返回 Unavailable Reset。GoAway 发送失败时再次调用 Drain 会重试。

`Close(ctx)` 由并发调用共享一次握手。首个调用发送 Close，等待匹配的 CloseAck，并使用
调用方 deadline 与 `CloseHandshakeTimeout` 中较早者。超时仍会完成本地资源释放。收到
peer Close 后先终止 stream，再发送同 Seq 的 Ack；Ack 完成前保留 route，完成后才删除。
Close/CloseAck 在 owner 中形成 route barrier，取消尚未发送的数据，避免数据越过关闭帧。

## 活动感知心跳

只有成功解码并通过协议校验的入站 frame 才刷新 `lastRecv`。请求、响应、Data 和其他控制
帧都等价于一次存活证明，因此活跃 RPC 不额外发送心跳包；发送成功本身不刷新存活时间。

连接静默达到 `HeartbeatInterval` 且没有 pending Ping 时发送一个 Ping。收到任意有效 frame
都会清除 pending Ping；收到 Ping 返回同 Seq Pong。静默达到 `PeerTimeout` 后 fail-closed：
客户端终止独占 owner，服务端删除 route 并唤醒等待者。客户端复用 owner 时钟，服务端由
listener 的单个 sweeper 扫描全部 route。

## Owner 双队列

owner 使用相互独立的 data queue 和 control queue：

- data：Request、Data、Response 等业务帧。
- control：WindowUpdate、Reset、GoAway、Close/Ack、Ping/Pong。
- 数据预算同时限制已准入 frame 数和编码后的真实字节数。
- reservation 从准入开始一直持有到发送成功、最终失败或发送前取消。
- data/control 各最多保留一个 EAGAIN head，不存在无界 pending slice。
- 每处理最多 8 个 control 后尝试一个 data，避免数据长期饥饿。
- 内部 Reset/Pong/Ack 非阻塞入 control queue；队列满或最终发送失败会 fail-close 相关 conn。
- Close barrier 封禁 route 的新数据并取消尚未发送的数据，然后发送 Close 或 CloseAck。

## Options 默认值

| Option | 默认值 |
| --- | ---: |
| SndHWM / RcvHWM | 1000 / 1000 |
| Linger | 1s |
| SendQueueSize | 1024 frames |
| SendQueueBytes | 64 MiB |
| ControlQueueSize | 128 frames |
| RecvQueueSize | 1024 frames |
| HandshakeTimeout | 1s |
| HeartbeatInterval | 10s |
| PeerTimeout | 30s |
| CloseHandshakeTimeout | 5s |

负数队列、字节、心跳和握手时间参数都会返回配置错误；`Linger` 例外，负值沿用
libzmq 的无限等待语义。`PeerTimeout` 必须至少是 `HeartbeatInterval` 的两倍。
`Metrics` 为 nil 时使用 noop collector。

## 错误语义

| 场景 | 错误或状态 | 资源动作 |
| --- | --- | --- |
| 接收/发送队列超限 | ResourceExhausted | Reset 或等待调用方 context |
| Drain 后新建 stream | Unavailable | 拒绝或 Reset |
| 已关闭 conn/stream | closed error | 不发送业务 frame |
| CloseAck 超时 | close handshake timeout | 完成本地关闭 |
| peer 心跳超时 | Unavailable | fail-closed 并删除 route |
| EAGAIN | 暂时错误 | 保留有界 head 并重试 |
| context 取消 | 原始 context error | 未发送请求最终释放预算 |

## 指标

`metrics.TransportEvent` 提供稳定事件类型：连接/stream 增减、队列拒绝、Heartbeat
Ping/Pong、peer timeout 和 route unavailable。事件包含 transport、value、connection ID、
stream ID 与可选 error。collector 同步调用，不在持有 conn/owner 锁时调用，也不会为每个
事件创建 goroutine；生产 collector 不应阻塞控制路径。

## 测试覆盖

- 控制帧字段校验和 Seq 规则。
- Options 默认值/非法值，以及 count+byte budget 的取消和 race。
- data/control 分类、EAGAIN reservation、8:1 调度、公平性、关闭 barrier 和失败清理。
- Drain、GoAway、并发 Close、Ack route 生命周期、调用方 deadline 和握手超时。
- 空闲 Ping/Pong 保活、异常 DEALER 断开后的 route 超时回收。
- 同一真实 ZMQ 连接连续 200 个 stream 后 map 归零，以及 50 次 client churn 后 route 归零。
- server accepted stream 所有权，以及 fake/ZeroMQ transport 回归。

## 剩余限制

- v1 不提供 TLS、mTLS 或 CURVE；应运行在可信网络或外部加密隧道中。
- `libzmq` 和 CGO 是部署前置条件。
- 单个 listener 的 ROUTER owner 串行处理 socket I/O，极端连接数下需要按 endpoint 分片。
- 当前 receive loop 使用固定短 poll 周期；低延迟与空闲 CPU 之间仍可继续调优。
- 非法或无法解码的 wire frame 会被丢弃，协议攻击检测需要在部署层补充。
