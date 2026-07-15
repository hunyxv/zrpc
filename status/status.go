package status

import (
	"context"
	"errors"
)

// Status 描述一次 RPC 调用的结果。
type Status struct {
	// Code 是 RPC 状态码。
	Code Code
	// Message 是面向调用方的错误消息。
	Message string
	// Details 携带可选的错误详情。
	Details []string
}

type rpcError struct {
	status Status
}

func (e *rpcError) Error() string {
	return e.status.Message
}

// Error 返回携带 RPC 状态的 error；OK 会返回 nil。
func Error(code Code, message string) error {
	if code == OK {
		return nil
	}
	return &rpcError{
		status: Status{
			Code:    code,
			Message: message,
		},
	}
}

// FromError 从 error 中提取 RPC 状态。
func FromError(err error) Status {
	if err == nil {
		return Status{Code: OK}
	}

	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		return rpcErr.status.copy()
	}

	if errors.Is(err, context.Canceled) {
		return Status{
			Code:    Canceled,
			Message: err.Error(),
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Status{
			Code:    DeadlineExceeded,
			Message: err.Error(),
		}
	}

	return Status{
		Code:    Unknown,
		Message: err.Error(),
	}
}

// WithDetails 返回带有详情副本的状态错误。
func WithDetails(err error, details ...string) error {
	st := FromError(err)
	if st.Code == OK {
		return nil
	}
	st.Details = append([]string(nil), details...)
	return &rpcError{status: st}
}

func (s Status) copy() Status {
	s.Details = append([]string(nil), s.Details...)
	return s
}
