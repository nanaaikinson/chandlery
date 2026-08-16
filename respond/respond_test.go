package respond

import (
	"errors"
	"testing"
)

func TestTypeForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   ErrorType
	}{
		{"bad request", 400, TypeBadRequest},
		{"unauthorized", 401, TypeUnauthorized},
		{"forbidden", 403, TypeForbidden},
		{"not found", 404, TypeNotFound},
		{"conflict", 409, TypeConflict},
		{"unprocessable entity", 422, TypeValidation},
		{"too many requests", 429, TypeRateLimited},
		{"internal server error", 500, TypeInternal},
		{"any other 5xx", 503, TypeInternal},
		{"unmapped 4xx", 418, ""},
		{"unmapped 2xx", 200, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TypeForStatus(tt.status); got != tt.want {
				t.Errorf("TypeForStatus(%d) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestBuilders(t *testing.T) {
	t.Parallel()

	if got := OK("done"); got != (Response{Message: "done"}) {
		t.Errorf("OK() = %+v, want message-only envelope", got)
	}

	got := Fail(TypeNotFound, "missing")
	want := Response{Message: "missing", Type: TypeNotFound}
	if got != want {
		t.Errorf("Fail() = %+v, want %+v", got, want)
	}

	details := []string{"required"}
	gotDetails := FailWithDetails(TypeValidation, "invalid", details)
	wantDetails := Response{Message: "invalid", Type: TypeValidation, Errors: details}
	if gotDetails.Message != wantDetails.Message || gotDetails.Type != wantDetails.Type {
		t.Errorf("FailWithDetails() = %+v, want %+v", gotDetails, wantDetails)
	}
	if got, ok := gotDetails.Errors.([]string); !ok || len(got) != 1 || got[0] != "required" {
		t.Errorf("FailWithDetails().Errors = %+v, want %+v", gotDetails.Errors, details)
	}

	// A typed-nil or empty errs must not surface as a populated Errors field
	// — encoding/json's own omitempty can't tell a typed-nil interface value
	// apart from a real one, so FailWithDetails has to check itself.
	var nilSlice []string
	gotNil := FailWithDetails(TypeValidation, "invalid", nilSlice)
	if gotNil.Errors != nil {
		t.Errorf("FailWithDetails(nil slice).Errors = %+v, want nil", gotNil.Errors)
	}

	gotEmpty := FailWithDetails(TypeValidation, "invalid", []string{})
	if gotEmpty.Errors != nil {
		t.Errorf("FailWithDetails(empty slice).Errors = %+v, want nil", gotEmpty.Errors)
	}

	gotInternal := Internal()
	wantInternal := Response{Message: InternalMessage, Type: TypeInternal}
	if gotInternal != wantInternal {
		t.Errorf("Internal() = %+v, want %+v", gotInternal, wantInternal)
	}
}

func TestStatusError(t *testing.T) {
	t.Parallel()

	t.Run("derives type from status", func(t *testing.T) {
		t.Parallel()
		err := NewStatusError(404, "no such order")
		if err.Type != TypeNotFound {
			t.Errorf("Type = %q, want %q", err.Type, TypeNotFound)
		}
		if err.Error() != "no such order" {
			t.Errorf("Error() = %q, want %q", err.Error(), "no such order")
		}
	})

	t.Run("wraps and unwraps a cause without leaking it into Error()", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("connection refused")
		err := &StatusError{Status: 500, Type: TypeInternal, Message: "lookup failed", Err: cause}

		if !errors.Is(err, cause) {
			t.Error("errors.Is(err, cause) = false, want true (Unwrap should expose the cause)")
		}
		if got := err.Error(); got != "lookup failed: connection refused" {
			t.Errorf("Error() = %q, want %q", got, "lookup failed: connection refused")
		}
	})

	t.Run("recognized via errors.AsType", func(t *testing.T) {
		t.Parallel()
		var err error = NewStatusError(409, "already exists")

		statusErr, ok := errors.AsType[*StatusError](err)
		if !ok {
			t.Fatal("errors.AsType[*StatusError] = false, want true")
		}
		if statusErr.Status != 409 {
			t.Errorf("Status = %d, want 409", statusErr.Status)
		}
	})

	t.Run("NewStatusError with a cause wraps it", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("connection refused")
		err := NewStatusError(500, "lookup failed", cause)

		if !errors.Is(err, cause) {
			t.Error("errors.Is(err, cause) = false, want true")
		}
		if got := err.Error(); got != "lookup failed: connection refused" {
			t.Errorf("Error() = %q, want %q", got, "lookup failed: connection refused")
		}
	})

	t.Run("NewStatusError with no cause has a nil Unwrap", func(t *testing.T) {
		t.Parallel()
		err := NewStatusError(404, "no such order")
		if err.Unwrap() != nil {
			t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
		}
	})
}
