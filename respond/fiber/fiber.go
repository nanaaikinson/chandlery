// Package fiber adapts the respond envelope to Fiber v3: it writes the JSON
// response and logs unexpected server errors before they're sent.
package fiber

import (
	"errors"
	"log/slog"

	gofiber "github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"

	"github.com/nanaaikinson/chandlery/respond"
)

// JSON writes an enveloped response with the given status.
func JSON(c gofiber.Ctx, status int, r respond.Response) error {
	return c.Status(status).JSON(r)
}

// Data writes a bare JSON payload with no envelope. Use it for resource
// reads/creates (a business, an order, a list) where the payload itself is
// the response body.
func Data(c gofiber.Ctx, status int, data any) error {
	return c.Status(status).JSON(data)
}

// OK writes a 200 carrying just a message.
func OK(c gofiber.Ctx, message string) error {
	return JSON(c, gofiber.StatusOK, respond.OK(message))
}

// Fail writes an enveloped client error. Use it for expected 4xx outcomes; it
// does not log, since those aren't server faults.
func Fail(c gofiber.Ctx, status int, t respond.ErrorType, message string) error {
	return JSON(c, status, respond.Fail(t, message))
}

// FailWithDetails is Fail plus a per-field validation errors payload.
func FailWithDetails(c gofiber.Ctx, status int, t respond.ErrorType, message string, errs any) error {
	return JSON(c, status, respond.FailWithDetails(t, message, errs))
}

// Internal logs an unexpected error against the current request and writes a
// generic 500 envelope. Handlers call this for errors they didn't map to a
// client status, so every 500 is logged with request context before it's sent.
func Internal(c gofiber.Ctx, err error) error {
	logServerError(c, err)
	return JSON(c, gofiber.StatusInternalServerError, respond.Internal())
}

// ErrorHandler renders any error that propagates up the middleware/handler
// stack as the standard envelope, and logs 5xx as a safety net (so an error
// that wasn't routed through Internal is still recorded). Wire it via
// gofiber.New(gofiber.Config{ErrorHandler: fiber.ErrorHandler}).
func ErrorHandler(c gofiber.Ctx, err error) error {
	status := gofiber.StatusInternalServerError
	message := respond.InternalMessage
	errType := respond.TypeInternal

	if statusErr, ok := errors.AsType[*respond.StatusError](err); ok && validStatus(statusErr.Status) {
		status = statusErr.Status
		errType = statusErr.Type
		if status < gofiber.StatusInternalServerError {
			message = statusErr.Message
		}
	} else if fiberErr, ok := errors.AsType[*gofiber.Error](err); ok {
		status = fiberErr.Code
		errType = respond.TypeForStatus(status)
		if status < gofiber.StatusInternalServerError {
			// Client errors carry a safe, caller-facing message; 5xx never do.
			message = fiberErr.Message
		}
	}

	if status >= gofiber.StatusInternalServerError {
		logServerError(c, err)
	}

	return JSON(c, status, respond.Fail(errType, message))
}

// validStatus guards against a hand-built *respond.StatusError that skipped
// NewStatusError and left Status at its zero value (or some other
// non-HTTP-status number) — falls back to the generic 500 rather than
// writing an invalid status like 0.
func validStatus(status int) bool {
	return status >= 100 && status <= 599
}

// logServerError records a server-side failure with request context. Unlike
// respond/nethttp (which has no standard request-ID mechanism and so exports
// WithRequestID/RequestIDFromContext of its own), this reads the ID via
// Fiber's own requestid middleware — wire that middleware in if you want one
// populated here; there's no chandlery-specific equivalent to add for Fiber.
func logServerError(c gofiber.Ctx, err error) {
	slog.Error("request failed",
		"method", c.Method(),
		"path", c.Path(),
		"request_id", requestid.FromContext(c),
		"error", err,
	)
}
