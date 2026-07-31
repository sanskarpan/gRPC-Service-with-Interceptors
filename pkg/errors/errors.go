// Package errors maps application failures to client-safe gRPC status values,
// including machine-readable google.rpc error details (ErrorInfo, BadRequest)
// so clients can branch on a stable reason and field-level violations rather
// than parsing human-readable messages.
package errors

import (
	stderrors "errors"
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"
)

// Domain is the error domain reported in ErrorInfo. Clients key their
// reason/domain lookups on this value.
const Domain = "userservice.example.v1"

// FieldViolation describes one invalid request field.
type FieldViolation struct {
	Field       string
	Description string
}

// AppError is a client-safe application error carrying a gRPC status code and,
// optionally, structured error details.
type AppError struct {
	Code    codes.Code
	Message string
	// Reason is a stable, machine-readable UPPER_SNAKE_CASE identifier
	// (e.g. FIELD_REQUIRED, USER_NOT_FOUND). Surfaced via ErrorInfo.
	Reason string
	// Metadata carries additional key/values on the ErrorInfo detail.
	Metadata map[string]string
	// FieldViolations, when present, are surfaced as a BadRequest detail.
	FieldViolations []FieldViolation
}

// Error implements the error interface without exposing internal state.
func (e *AppError) Error() string { return e.Message }

// WithReason sets the machine-readable ErrorInfo reason and returns the error
// for fluent construction.
func (e *AppError) WithReason(reason string) *AppError {
	e.Reason = reason
	return e
}

// WithMetadata adds a key/value pair to the ErrorInfo metadata.
func (e *AppError) WithMetadata(key, value string) *AppError {
	if e.Metadata == nil {
		e.Metadata = map[string]string{}
	}
	e.Metadata[key] = value
	return e
}

// WithField records a field-level violation surfaced as a BadRequest detail.
func (e *AppError) WithField(field, description string) *AppError {
	e.FieldViolations = append(e.FieldViolations, FieldViolation{Field: field, Description: description})
	return e
}

// GRPCStatus lets gRPC preserve the application status code and details at the
// boundary. ErrorInfo and BadRequest details are attached best-effort; if
// marshalling a detail fails the base status is still returned.
func (e *AppError) GRPCStatus() *status.Status {
	st := status.New(e.Code, e.Message)
	// The generated errdetails messages already satisfy protoadapt.MessageV1
	// (Reset/String/ProtoMessage), so they can be appended directly.
	var details []protoadapt.MessageV1

	if e.Reason != "" || len(e.Metadata) > 0 {
		details = append(details, &errdetails.ErrorInfo{
			Reason:   reasonOrDefault(e),
			Domain:   Domain,
			Metadata: e.Metadata,
		})
	}
	if len(e.FieldViolations) > 0 {
		br := &errdetails.BadRequest{}
		for _, fv := range e.FieldViolations {
			br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
				Field:       fv.Field,
				Description: fv.Description,
			})
		}
		details = append(details, br)
	}
	if len(details) == 0 {
		return st
	}
	if withDetails, err := st.WithDetails(details...); err == nil {
		return withDetails
	}
	return st
}

// ErrInvalidArgument reports invalid caller input.
func ErrInvalidArgument(msg string) *AppError {
	return &AppError{Code: codes.InvalidArgument, Message: msg}
}

// ErrNotFound reports a missing resource.
func ErrNotFound(msg string) *AppError {
	return &AppError{Code: codes.NotFound, Message: msg}
}

// ErrAlreadyExists reports a conflicting resource.
func ErrAlreadyExists(msg string) *AppError {
	return &AppError{Code: codes.AlreadyExists, Message: msg}
}

// ErrUnauthenticated reports missing or invalid credentials.
func ErrUnauthenticated(msg string) *AppError {
	return &AppError{Code: codes.Unauthenticated, Message: msg}
}

// ErrPermissionDenied reports an authorization failure.
func ErrPermissionDenied(msg string) *AppError {
	return &AppError{Code: codes.PermissionDenied, Message: msg}
}

// ErrInternal reports a server-side failure with a safe client message.
func ErrInternal(msg string) *AppError {
	return &AppError{Code: codes.Internal, Message: msg}
}

// ErrResourceExhausted reports a configured resource limit.
func ErrResourceExhausted(msg string) *AppError {
	return &AppError{Code: codes.ResourceExhausted, Message: msg}
}

// ErrUnavailable reports a temporarily unavailable dependency or service.
func ErrUnavailable(msg string) *AppError {
	return &AppError{Code: codes.Unavailable, Message: msg}
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
func New(code codes.Code, format string, args ...interface{}) *AppError {
	return &AppError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func reasonOrDefault(e *AppError) string {
	if e.Reason != "" {
		return e.Reason
	}
	return e.Code.String()
}
