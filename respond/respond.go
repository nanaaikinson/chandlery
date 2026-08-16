// Package respond defines the standard JSON response envelope shared across
// HTTP frameworks. It is framework-agnostic: it builds response bodies and
// classifies errors, but never writes to a wire. Framework adapters
// (respond/fiber, respond/nethttp, ...) do the actual writing and logging.
package respond

import "reflect"

// InternalMessage is the caller-facing text for any 5xx; the real cause is
// logged by the adapter, never returned, so internal detail can't leak to
// clients.
const InternalMessage = "Something went wrong on our end. Please try again."

// ErrorType is a stable, machine-readable classifier a client can branch on
// without parsing the human-readable Message.
type ErrorType string

const (
	TypeBadRequest   ErrorType = "bad_request"
	TypeValidation   ErrorType = "validation_error"
	TypeUnauthorized ErrorType = "unauthorized"
	TypeForbidden    ErrorType = "forbidden"
	TypeNotFound     ErrorType = "not_found"
	TypeConflict     ErrorType = "conflict"
	TypeRateLimited  ErrorType = "rate_limited"
	TypeInternal     ErrorType = "internal_error"
)

// Response is the envelope every message/error response uses. Errors and Type
// are omitted when empty, so a plain success is just {"message": "..."}.
type Response struct {
	Message string    `json:"message"`
	Errors  any       `json:"errors,omitempty"`
	Type    ErrorType `json:"type,omitempty"`
}

// OK builds a 200 envelope carrying just a message.
func OK(message string) Response {
	return Response{Message: message}
}

// Fail builds an enveloped client-error body. Use it for expected 4xx
// outcomes; adapters don't log these, since they aren't server faults.
func Fail(t ErrorType, message string) Response {
	return Response{Message: message, Type: t}
}

// FailWithDetails is Fail plus a per-field validation errors payload. errs is
// omitted from the response if it's nil or an empty/nil slice, map, or
// pointer — encoding/json's own omitempty can't tell a typed-nil interface
// value apart from a populated one, so this checks before Errors is ever set.
func FailWithDetails(t ErrorType, message string, errs any) Response {
	r := Response{Message: message, Type: t}
	if !isEmpty(errs) {
		r.Errors = errs
	}
	return r
}

// isEmpty reports whether v is nil, or a typed-nil/zero-length
// slice/map/pointer/chan/func hiding behind the any it's boxed in.
func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return rv.IsNil() || rv.Len() == 0
	case reflect.Ptr, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// Internal builds the generic 500 envelope. Adapters log the real error
// against the request before sending this.
func Internal() Response {
	return Response{Message: InternalMessage, Type: TypeInternal}
}

// TypeForStatus maps an HTTP status code onto its ErrorType, so adapters'
// generic error handlers can classify errors they didn't originate. Status
// codes are spelled out as literals (not net/http's constants) so this core
// package stays free of any framework/transport import.
func TypeForStatus(status int) ErrorType {
	switch status {
	case 400:
		return TypeBadRequest
	case 401:
		return TypeUnauthorized
	case 403:
		return TypeForbidden
	case 404:
		return TypeNotFound
	case 409:
		return TypeConflict
	case 422:
		return TypeValidation
	case 429:
		return TypeRateLimited
	default:
		if status >= 500 {
			return TypeInternal
		}
		return ""
	}
}

// StatusError is a framework-independent error carrying the HTTP status and
// caller-facing message it should render as. Handlers can return it directly;
// every adapter's generic error handler recognizes it via errors.As, the same
// way it recognizes its own framework's native HTTP error type.
type StatusError struct {
	Status  int
	Type    ErrorType
	Message string
	Err     error // optional wrapped cause, logged but never sent to the client
}

// NewStatusError builds a StatusError, deriving its Type from Status. Pass a
// cause to wrap it — errors.Is/errors.As can then reach it through Unwrap —
// e.g. NewStatusError(500, "lookup failed", sql.ErrNoRows). At most one cause
// is used; extra arguments are ignored.
func NewStatusError(status int, message string, cause ...error) *StatusError {
	e := &StatusError{Status: status, Type: TypeForStatus(status), Message: message}
	if len(cause) > 0 {
		e.Err = cause[0]
	}
	return e
}

func (e *StatusError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *StatusError) Unwrap() error {
	return e.Err
}
