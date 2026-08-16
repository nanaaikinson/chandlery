// Package nethttp adapts the respond envelope to net/http: it writes the
// JSON response and logs unexpected server errors before they're sent.
package nethttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nanaaikinson/chandlery/respond"
)

// Handler is like http.HandlerFunc but returns an error. Return a
// *respond.StatusError for expected client errors, or any other error for
// unexpected failures; Wrap renders both as the standard envelope and logs
// the ones that are 5xx.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Wrap adapts a Handler into an http.HandlerFunc, giving net/http the same
// centralized error handling Fiber gets from its ErrorHandler config: every
// error a handler returns is classified, logged if it's a 5xx, and rendered
// as the standard envelope.
func Wrap(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			handleError(w, r, err)
		}
	}
}

// handleError classifies err, logs it if it's a 5xx, and writes the envelope.
func handleError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := respond.InternalMessage
	errType := respond.TypeInternal

	if statusErr, ok := errors.AsType[*respond.StatusError](err); ok && validStatus(statusErr.Status) {
		status = statusErr.Status
		errType = statusErr.Type
		if status < http.StatusInternalServerError {
			message = statusErr.Message
		}
	}

	if status >= http.StatusInternalServerError {
		logServerError(r, err)
	}

	if writeErr := JSON(w, status, respond.Fail(errType, message)); writeErr != nil {
		slog.Error("failed to write error response", "method", r.Method, "path", r.URL.Path, "error", writeErr)
	}
}

// validStatus guards against a hand-built *respond.StatusError that skipped
// NewStatusError and left Status at its zero value (or some other
// non-HTTP-status number) — falls back to the generic 500 rather than
// writing an invalid status like 0.
func validStatus(status int) bool {
	return status >= 100 && status <= 599
}

// JSON writes an enveloped response with the given status.
func JSON(w http.ResponseWriter, status int, r respond.Response) error {
	return writeJSON(w, status, r)
}

// Data writes a bare JSON payload with no envelope. Use it for resource
// reads/creates (a business, an order, a list) where the payload itself is
// the response body.
func Data(w http.ResponseWriter, status int, data any) error {
	return writeJSON(w, status, data)
}

// OK writes a 200 carrying just a message.
func OK(w http.ResponseWriter, message string) error {
	return JSON(w, http.StatusOK, respond.OK(message))
}

// Fail writes an enveloped client error. Use it for expected 4xx outcomes; it
// does not log, since those aren't server faults.
func Fail(w http.ResponseWriter, status int, t respond.ErrorType, message string) error {
	return JSON(w, status, respond.Fail(t, message))
}

// FailWithDetails is Fail plus a per-field validation errors payload.
func FailWithDetails(w http.ResponseWriter, status int, t respond.ErrorType, message string, errs any) error {
	return JSON(w, status, respond.FailWithDetails(t, message, errs))
}

// Internal logs an unexpected error against the current request and writes a
// generic 500 envelope. Handlers call this for errors they didn't map to a
// client status, so every 500 is logged with request context before it's sent.
func Internal(w http.ResponseWriter, r *http.Request, err error) error {
	logServerError(r, err)
	return JSON(w, http.StatusInternalServerError, respond.Internal())
}

// writeJSON returns the encode error (e.g. a client that disconnected
// mid-response) instead of discarding it, so callers can log or otherwise
// react to a write that didn't actually reach the client.
func writeJSON(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}

type requestIDKey struct{}

// WithRequestID stores a request ID on ctx for logServerError to pick up.
// Wire it into whatever middleware generates the ID for a request.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request ID stored by WithRequestID, or ""
// if none was set.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// logServerError records a server-side failure with request context.
func logServerError(r *http.Request, err error) {
	slog.Error("request failed",
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", RequestIDFromContext(r.Context()),
		"error", err,
	)
}
