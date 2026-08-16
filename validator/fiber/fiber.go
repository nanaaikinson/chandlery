// Package fiber provides Fiber v3 request-validation middleware built on
// zog schemas and chandlery's respond envelope.
package fiber

import (
	"fmt"

	"github.com/Oudwins/zog"
	gofiber "github.com/gofiber/fiber/v3"

	"github.com/nanaaikinson/chandlery/respond"
	respondfiber "github.com/nanaaikinson/chandlery/respond/fiber"
	"github.com/nanaaikinson/chandlery/validator"
)

// localsKey is the c.Locals key ValidateRequest[T] stores under. It's
// parameterized by T (a distinct zero-size type per instantiation) rather
// than a single shared key, so validating more than one type on the same
// route/chain (e.g. a body and a separately-validated query struct) can't
// clobber each other.
type localsKey[T any] struct{}

// ValidateRequest binds the request body into a new T, validates it against
// schema, and stores it for Body[T] to retrieve. A malformed body renders as
// 400; a failed validation renders as 422 with per-field details.
func ValidateRequest[T any](schema *zog.StructSchema) gofiber.Handler {
	return func(c gofiber.Ctx) error {
		data := new(T)

		// A bind failure means the request body itself is malformed (bad JSON,
		// wrong content-type) — a 400, not a 500. The raw bind error is not
		// returned to the client, since it can echo internal field names.
		if err := c.Bind().Body(data); err != nil {
			return respondfiber.Fail(c, gofiber.StatusBadRequest, respond.TypeBadRequest, "Malformed request body")
		}

		if errs := schema.Validate(data); len(errs) > 0 {
			return respondfiber.FailWithDetails(c, gofiber.StatusUnprocessableEntity,
				respond.TypeValidation, "Validation failed", validator.SanitizeIssues(errs))
		}

		c.Locals(localsKey[T]{}, data)

		return c.Next()
	}
}

// Body returns the request body ValidateRequest[T] stored for this request.
// T must match the type ValidateRequest was registered with on this route.
func Body[T any](c gofiber.Ctx) T {
	data, ok := c.Locals(localsKey[T]{}).(*T)
	if !ok {
		panic(fmt.Sprintf("validator/fiber.Body[%T]: no validated body of this type for this request; is ValidateRequest[%T] registered on this route?", *new(T), *new(T)))
	}
	return *data
}
