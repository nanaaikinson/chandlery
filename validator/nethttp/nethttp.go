// Package nethttp provides net/http request-validation middleware built on
// zog schemas and chandlery's respond envelope.
package nethttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Oudwins/zog"

	"github.com/nanaaikinson/chandlery/respond"
	respondhttp "github.com/nanaaikinson/chandlery/respond/nethttp"
	"github.com/nanaaikinson/chandlery/validator"
)

// contextKey is the context.WithValue key ValidateRequest[T] stores under.
// It's parameterized by T (a distinct zero-size type per instantiation)
// rather than a single shared key, so validating more than one type on the
// same request (e.g. a body and a separately-validated query struct) can't
// clobber each other.
type contextKey[T any] struct{}

// ValidateRequest returns net/http middleware that decodes the request body
// into a new T, validates it against schema, and stores it in the request
// context for Body[T] to retrieve. A malformed body renders as 400; a failed
// validation renders as 422 with per-field details.
func ValidateRequest[T any](schema *zog.StructSchema) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data := new(T)

			// A decode failure means the request body itself is malformed (bad
			// JSON, wrong content-type) — a 400, not a 500. The raw decode error
			// is not returned to the client, since it can echo internal field
			// names.
			if err := json.NewDecoder(r.Body).Decode(data); err != nil {
				respondhttp.Fail(w, http.StatusBadRequest, respond.TypeBadRequest, "Malformed request body")
				return
			}

			if errs := schema.Validate(data); len(errs) > 0 {
				respondhttp.FailWithDetails(w, http.StatusUnprocessableEntity,
					respond.TypeValidation, "Validation failed", validator.SanitizeIssues(errs))
				return
			}

			ctx := context.WithValue(r.Context(), contextKey[T]{}, data)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Body returns the request body ValidateRequest[T] stored for this request.
// T must match the type ValidateRequest was registered with on this route.
func Body[T any](r *http.Request) T {
	data, ok := r.Context().Value(contextKey[T]{}).(*T)
	if !ok {
		panic(fmt.Sprintf("validator/nethttp.Body[%T]: no validated body of this type for this request; is ValidateRequest[%T] registered on this route?", *new(T), *new(T)))
	}
	return *data
}
