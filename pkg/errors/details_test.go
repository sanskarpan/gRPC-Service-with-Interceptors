package errors

import (
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppErrorCarriesErrorInfoAndBadRequest(t *testing.T) {
	err := ErrInvalidArgument("id is required").
		WithReason("FIELD_REQUIRED").
		WithMetadata("rpc", "GetUser").
		WithField("id", "must not be empty")

	st, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected a gRPC status")
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", st.Code())
	}

	var sawInfo, sawBadRequest bool
	for _, d := range st.Details() {
		switch v := d.(type) {
		case *errdetails.ErrorInfo:
			sawInfo = true
			if v.GetReason() != "FIELD_REQUIRED" {
				t.Errorf("reason = %q, want FIELD_REQUIRED", v.GetReason())
			}
			if v.GetDomain() != Domain {
				t.Errorf("domain = %q, want %q", v.GetDomain(), Domain)
			}
			if v.GetMetadata()["rpc"] != "GetUser" {
				t.Errorf("metadata rpc = %q, want GetUser", v.GetMetadata()["rpc"])
			}
		case *errdetails.BadRequest:
			sawBadRequest = true
			if len(v.GetFieldViolations()) != 1 || v.GetFieldViolations()[0].GetField() != "id" {
				t.Errorf("field violations = %+v, want one on 'id'", v.GetFieldViolations())
			}
		}
	}
	if !sawInfo {
		t.Error("expected an ErrorInfo detail")
	}
	if !sawBadRequest {
		t.Error("expected a BadRequest detail")
	}
}

func TestAppErrorWithoutDetailsStaysPlain(t *testing.T) {
	st, _ := status.FromError(ErrNotFound("user not found"))
	if len(st.Details()) != 0 {
		t.Fatalf("expected no details, got %d", len(st.Details()))
	}
}
