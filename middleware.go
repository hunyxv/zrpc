package zrpc

// LoadTaskContext 加载任务上下文
type LoadTaskContext func(Header)

// GetTaskContext 生成任务上下文
type GetTaskContext func(Header)

// ServerBeforeCall 服务端调用 function 前执行
type ServerBeforeCall func(p *Pack)

// ServerAfterCall 服务端调用 function 后执行
type ServerAfterCall func(req *Pack, rep *Pack)

// ServerRecover 服务端调用发生异常时执行
type ServerRecover func(req *Pack, rep *Pack, h Header)

// ClientBeforeCall 客户端调用远端 function 前执行
type ClientBeforeCall func(req *Pack)

// ClientAfterCall 客户端调用远端 function 后执行
type ClientAfterCall func(req, rep *Pack)
