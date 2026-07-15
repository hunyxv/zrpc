package status

// Code 标识 RPC 结果类别。
type Code int

const (
	// OK 表示调用成功。
	OK Code = iota
	// Canceled 表示调用被调用方取消。
	Canceled
	// Unknown 表示未知错误。
	Unknown
	// InvalidArgument 表示请求参数非法。
	InvalidArgument
	// DeadlineExceeded 表示调用超过截止时间。
	DeadlineExceeded
	// NotFound 表示请求的资源不存在。
	NotFound
	// AlreadyExists 表示资源已存在。
	AlreadyExists
	// PermissionDenied 表示权限不足。
	PermissionDenied
	// ResourceExhausted 表示资源耗尽或触发限流。
	ResourceExhausted
	// FailedPrecondition 表示当前状态不满足调用前置条件。
	FailedPrecondition
	// Aborted 表示操作被并发冲突或事务中止打断。
	Aborted
	// OutOfRange 表示参数超出允许范围。
	OutOfRange
	// Unimplemented 表示方法或能力未实现。
	Unimplemented
	// Internal 表示框架或服务端内部错误。
	Internal
	// Unavailable 表示服务暂不可用。
	Unavailable
	// DataLoss 表示发生不可恢复的数据损坏或丢失。
	DataLoss
	// Unauthenticated 表示调用方未通过认证。
	Unauthenticated
)
