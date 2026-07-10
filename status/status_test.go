package status

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestStatusErrorRoundTrip(t *testing.T) {
	err := Error(InvalidArgument, "bad request")
	if err == nil {
		t.Fatal("Error() returned nil")
	}
	if got := err.Error(); got != "bad request" {
		t.Fatalf("Error().Error() = %q, want %q", got, "bad request")
	}

	st := FromError(err)
	if st.Code != InvalidArgument {
		t.Fatalf("FromError(err).Code = %v, want %v", st.Code, InvalidArgument)
	}
	if st.Message != "bad request" {
		t.Fatalf("FromError(err).Message = %q, want %q", st.Message, "bad request")
	}
	if len(st.Details) != 0 {
		t.Fatalf("FromError(err).Details = %#v, want empty", st.Details)
	}
}

func TestUnknownForPlainError(t *testing.T) {
	ok := FromError(nil)
	if ok.Code != OK || ok.Message != "" || len(ok.Details) != 0 {
		t.Fatalf("FromError(nil) = %#v, want OK empty status", ok)
	}

	err := errors.New("plain failure")
	st := FromError(err)
	if st.Code != Unknown {
		t.Fatalf("FromError(plain).Code = %v, want %v", st.Code, Unknown)
	}
	if st.Message != "plain failure" {
		t.Fatalf("FromError(plain).Message = %q, want %q", st.Message, "plain failure")
	}
	if len(st.Details) != 0 {
		t.Fatalf("FromError(plain).Details = %#v, want empty", st.Details)
	}
}

func TestWithDetails(t *testing.T) {
	details := []string{"id=42", "resource=user"}
	err := WithDetails(Error(NotFound, "missing"), details...)
	details[0] = "changed"

	st := FromError(err)
	if st.Code != NotFound {
		t.Fatalf("WithDetails status code = %v, want %v", st.Code, NotFound)
	}
	if st.Message != "missing" {
		t.Fatalf("WithDetails status message = %q, want %q", st.Message, "missing")
	}
	if !slices.Equal(st.Details, []string{"id=42", "resource=user"}) {
		t.Fatalf("WithDetails status details = %#v, want %#v", st.Details, []string{"id=42", "resource=user"})
	}
}

func TestContextErrorsMapToStatusCodes(t *testing.T) {
	if st := FromError(context.Canceled); st.Code != Canceled {
		t.Fatalf("FromError(context.Canceled).Code = %v, want %v", st.Code, Canceled)
	}
	if st := FromError(context.DeadlineExceeded); st.Code != DeadlineExceeded {
		t.Fatalf("FromError(context.DeadlineExceeded).Code = %v, want %v", st.Code, DeadlineExceeded)
	}
	wrapped := errors.Join(errors.New("outer"), context.Canceled)
	if st := FromError(wrapped); st.Code != Canceled {
		t.Fatalf("FromError(wrapped canceled).Code = %v, want %v", st.Code, Canceled)
	}
}

func TestOKStatusIsNilError(t *testing.T) {
	if err := Error(OK, "ignored"); err != nil {
		t.Fatalf("Error(OK) = %v, want nil", err)
	}
	if err := WithDetails(nil, "ignored"); err != nil {
		t.Fatalf("WithDetails(nil) = %v, want nil", err)
	}
}
