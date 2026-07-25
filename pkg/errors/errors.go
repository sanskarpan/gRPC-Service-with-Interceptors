// Package errors maps application failures to client-safe gRPC status values.
package errors

import (
	stderrors "errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AppError is a client-safe application error carrying a gRPC status code.
type AppError struct {
	Code    codes.Code
	Message string
}

// Error implements the error interface without exposing internal state.
func (e *AppError) Error() string {
	return e.Message
}

// GRPCStatus lets gRPC preserve the application status code at the boundary.
func (e *AppError) GRPCStatus() *status.Status {
	return status.New(e.Code, e.Message)
}

// ErrInvalidArgument reports invalid caller input.
func ErrInvalidArgument(msg string) error {
	return &AppError{
		Code:    codes.InvalidArgument,
		Message: msg,
	}
}

// ErrNotFound reports a missing resource.
func ErrNotFound(msg string) error {
	return &AppError{
		Code:    codes.NotFound,
		Message: msg,
	}
}

// ErrAlreadyExists reports a conflicting resource.
func ErrAlreadyExists(msg string) error {
	return &AppError{
		Code:    codes.AlreadyExists,
		Message: msg,
	}
}

// ErrUnauthenticated reports missing or invalid credentials.
func ErrUnauthenticated(msg string) error {
	return &AppError{
		Code:    codes.Unauthenticated,
		Message: msg,
	}
}

// ErrPermissionDenied reports an authorization failure.
func ErrPermissionDenied(msg string) error {
	return &AppError{
		Code:    codes.PermissionDenied,
		Message: msg,
	}
}

// ErrInternal reports a server-side failure with a safe client message.
func ErrInternal(msg string) error {
	return &AppError{
		Code:    codes.Internal,
		Message: msg,
	}
}

// ErrResourceExhausted reports a configured resource limit.
func ErrResourceExhausted(msg string) error {
	return &AppError{
		Code:    codes.ResourceExhausted,
		Message: msg,
	}
}

// ErrUnavailable reports a temporarily unavailable dependency or service.
func ErrUnavailable(msg string) error {
	return &AppError{
		Code:    codes.Unavailable,
		Message: msg,
	}
}

// FromError converts an error into a client-safe gRPC code and message.
func FromError(err error) (codes.Code, string) {
	if err == nil {
		return codes.OK, ""
	}
	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr.Code, appErr.Message
	}
	return codes.Unknown, "internal server error"
}

// New constructs a formatted client-safe application error.
func New(code codes.Code, format string, args ...interface{}) error {
	return &AppError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}
