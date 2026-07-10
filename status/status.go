package status

import "errors"

// Status describes the outcome of an RPC call.
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

// Error returns an error carrying an RPC status.
func Error(code Code, message string) error {
	return &rpcError{
		status: Status{
			Code:    code,
			Message: message,
		},
	}
}

// FromError returns the RPC status represented by err.
func FromError(err error) Status {
	if err == nil {
		return Status{Code: OK}
	}

	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		return rpcErr.status.copy()
	}

	return Status{
		Code:    Unknown,
		Message: err.Error(),
	}
}

// WithDetails returns err's status with details replaced by a copied slice.
func WithDetails(err error, details ...string) error {
	st := FromError(err)
	st.Details = append([]string(nil), details...)
	return &rpcError{status: st}
}

func (s Status) copy() Status {
	s.Details = append([]string(nil), s.Details...)
	return s
}
