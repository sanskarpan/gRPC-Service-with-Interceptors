package errors

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFromErrorIsNilSafeAndClientSafe(t *testing.T) {
	if code, message := FromError(nil); code != codes.OK || message != "" {
		t.Fatalf("FromError(nil) = %v, %q", code, message)
	}
	wrapped := fmt.Errorf("wrapped: %w", ErrNotFound("user not found"))
	if code, message := FromError(wrapped); code != codes.NotFound || message != "user not found" {
		t.Fatalf("FromError(wrapped) = %v, %q", code, message)
	}
	if code, message := FromError(fmt.Errorf("database password=secret")); code != codes.Unknown || message != "internal server error" {
		t.Fatalf("FromError(unknown) = %v, %q", code, message)
	}
}

func TestErrInvalidArgument(t *testing.T) {
	t.Parallel()
	err := ErrInvalidArgument("invalid input")
	if err == nil {
		t.Fatal("ErrInvalidArgument() returned nil")
	}
	code, message := FromError(err)
	if code != codes.InvalidArgument {
		t.Errorf("code = %v, want %v", code, codes.InvalidArgument)
	}
	if message != "invalid input" {
		t.Errorf("message = %q, want %q", message, "invalid input")
	}
}

func TestErrNotFound(t *testing.T) {
	t.Parallel()
	err := ErrNotFound("user missing")
	if err == nil {
		t.Fatal("ErrNotFound() returned nil")
	}
	code, message := FromError(err)
	if code != codes.NotFound {
		t.Errorf("code = %v, want %v", code, codes.NotFound)
	}
	if message != "user missing" {
		t.Errorf("message = %q, want %q", message, "user missing")
	}
}

func TestErrAlreadyExists(t *testing.T) {
	t.Parallel()
	err := ErrAlreadyExists("dup")
	if err == nil {
		t.Fatal("ErrAlreadyExists() returned nil")
	}
	code, message := FromError(err)
	if code != codes.AlreadyExists {
		t.Errorf("code = %v, want %v", code, codes.AlreadyExists)
	}
	if message != "dup" {
		t.Errorf("message = %q, want %q", message, "dup")
	}
}

func TestErrUnauthenticated(t *testing.T) {
	t.Parallel()
	err := ErrUnauthenticated("no creds")
	if err == nil {
		t.Fatal("ErrUnauthenticated() returned nil")
	}
	code, message := FromError(err)
	if code != codes.Unauthenticated {
		t.Errorf("code = %v, want %v", code, codes.Unauthenticated)
	}
	if message != "no creds" {
		t.Errorf("message = %q, want %q", message, "no creds")
	}
}

func TestErrPermissionDenied(t *testing.T) {
	t.Parallel()
	err := ErrPermissionDenied("forbidden")
	if err == nil {
		t.Fatal("ErrPermissionDenied() returned nil")
	}
	code, message := FromError(err)
	if code != codes.PermissionDenied {
		t.Errorf("code = %v, want %v", code, codes.PermissionDenied)
	}
	if message != "forbidden" {
		t.Errorf("message = %q, want %q", message, "forbidden")
	}
}

func TestErrInternal(t *testing.T) {
	t.Parallel()
	err := ErrInternal("boom")
	if err == nil {
		t.Fatal("ErrInternal() returned nil")
	}
	code, message := FromError(err)
	if code != codes.Internal {
		t.Errorf("code = %v, want %v", code, codes.Internal)
	}
	if message != "boom" {
		t.Errorf("message = %q, want %q", message, "boom")
	}
}

func TestErrResourceExhausted(t *testing.T) {
	t.Parallel()
	err := ErrResourceExhausted("full")
	if err == nil {
		t.Fatal("ErrResourceExhausted() returned nil")
	}
	code, message := FromError(err)
	if code != codes.ResourceExhausted {
		t.Errorf("code = %v, want %v", code, codes.ResourceExhausted)
	}
	if message != "full" {
		t.Errorf("message = %q, want %q", message, "full")
	}
}

func TestErrUnavailable(t *testing.T) {
	t.Parallel()
	err := ErrUnavailable("down")
	if err == nil {
		t.Fatal("ErrUnavailable() returned nil")
	}
	code, message := FromError(err)
	if code != codes.Unavailable {
		t.Errorf("code = %v, want %v", code, codes.Unavailable)
	}
	if message != "down" {
		t.Errorf("message = %q, want %q", message, "down")
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		code        codes.Code
		format      string
		args        []interface{}
		wantCode    codes.Code
		wantMessage string
	}{
		{
			name:        "simple format",
			code:        codes.InvalidArgument,
			format:      "bad value: %s",
			args:        []interface{}{"abc"},
			wantCode:    codes.InvalidArgument,
			wantMessage: "bad value: abc",
		},
		{
			name:        "integer format",
			code:        codes.NotFound,
			format:      "missing item %d of %d",
			args:        []interface{}{1, 5},
			wantCode:    codes.NotFound,
			wantMessage: "missing item 1 of 5",
		},
		{
			name:        "no args",
			code:        codes.Internal,
			format:      "internal failure",
			args:        nil,
			wantCode:    codes.Internal,
			wantMessage: "internal failure",
		},
		{
			name:        "multiple types",
			code:        codes.PermissionDenied,
			format:      "user %s (id=%d) is forbidden",
			args:        []interface{}{"alice", 42},
			wantCode:    codes.PermissionDenied,
			wantMessage: "user alice (id=42) is forbidden",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := New(tt.code, tt.format, tt.args...)
			if err == nil {
				t.Fatal("New() returned nil")
			}
			code, message := FromError(err)
			if code != tt.wantCode {
				t.Errorf("code = %v, want %v", code, tt.wantCode)
			}
			if message != tt.wantMessage {
				t.Errorf("message = %q, want %q", message, tt.wantMessage)
			}
		})
	}
}

func TestAppErrorGRPCStatus(t *testing.T) {
	t.Parallel()
	appErr := &AppError{Code: codes.NotFound, Message: "user not found"}
	st := appErr.GRPCStatus()
	if st == nil {
		t.Fatal("GRPCStatus() returned nil")
	}
	if got := st.Code(); got != codes.NotFound {
		t.Errorf("status code = %v, want %v", got, codes.NotFound)
	}
	if got := st.Message(); got != "user not found" {
		t.Errorf("status message = %q, want %q", got, "user not found")
	}
}

func TestAppErrorGRPCStatusViaStatusError(t *testing.T) {
	t.Parallel()
	err := ErrInvalidArgument("bad")
	// Ensure status.FromError recognizes the AppError via GRPCStatus().
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("status code = %v, want %v", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "bad" {
		t.Errorf("status message = %q, want %q", st.Message(), "bad")
	}
}

func TestAppErrorError(t *testing.T) {
	t.Parallel()
	appErr := &AppError{Code: codes.Internal, Message: "internal server error"}
	if got := appErr.Error(); got != "internal server error" {
		t.Errorf("Error() = %q, want %q", got, "internal server error")
	}
	if got := ErrUnavailable("service down").Error(); got != "service down" {
		t.Errorf("helper Error() = %q, want %q", got, "service down")
	}
}

func TestFromErrorAppError(t *testing.T) {
	t.Parallel()
	appErr := &AppError{Code: codes.PermissionDenied, Message: "no access"}
	code, message := FromError(appErr)
	if code != codes.PermissionDenied {
		t.Errorf("code = %v, want %v", code, codes.PermissionDenied)
	}
	if message != "no access" {
		t.Errorf("message = %q, want %q", message, "no access")
	}
}

func TestFromErrorWrapped(t *testing.T) {
	t.Parallel()
	inner := ErrNotFound("user not found")
	wrapped := fmt.Errorf("wrapped: %w", inner)
	code, message := FromError(wrapped)
	if code != codes.NotFound {
		t.Errorf("code = %v, want %v", code, codes.NotFound)
	}
	if message != "user not found" {
		t.Errorf("message = %q, want %q", message, "user not found")
	}

	// Double-wrapping should still resolve through errors.As.
	doubleWrapped := fmt.Errorf("outer: %w", wrapped)
	code, message = FromError(doubleWrapped)
	if code != codes.NotFound {
		t.Errorf("double-wrapped code = %v, want %v", code, codes.NotFound)
	}
	if message != "user not found" {
		t.Errorf("double-wrapped message = %q, want %q", message, "user not found")
	}
}

func TestFromErrorUnknown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"plain string error", fmt.Errorf("database password=secret")},
		{"stdlib errors.New wrapped", fmt.Errorf("lower: %w", fmt.Errorf("some unknown failure"))},
		{"sentinel-style error", fmt.Errorf("something bad happened")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, message := FromError(tt.err)
			if code != codes.Unknown {
				t.Errorf("code = %v, want %v", code, codes.Unknown)
			}
			if message != "internal server error" {
				t.Errorf("message = %q, want %q", message, "internal server error")
			}
		})
	}
}
