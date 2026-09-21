package odm

import "fmt"

// Scope is a reusable query transformation — a named piece of filtering kept
// next to the model instead of repeated at every call site:
//
//	func Active(q *odm.Query[User]) *odm.Query[User] {
//		return q.Where("is_active", true)
//	}
//
//	users.Scope(Active).Get(ctx)
//
// A scope is an ordinary function over an immutable query, which is all it
// needs to be: it can't perform I/O (it has no context and returns no
// error), it can't mutate the query it was handed, and it composes with
// anything else in the chain.
type Scope[T any] func(*Query[T]) *Query[T]

// Scope applies each scope in turn, left to right. The query each one
// returns is what the next receives, so scopes compose the same way hand
// written conditions do:
//
//	users.Scope(Active, Verified).Where("country", "GH").Get(ctx)
//
// A nil scope records ErrInvalidQuery rather than panicking mid-chain.
func (q *Query[T]) Scope(scopes ...Scope[T]) *Query[T] {
	next := q
	for i, scope := range scopes {
		if scope == nil {
			return q.withError(fmt.Errorf("%w: Scope: scope %d is nil", ErrInvalidQuery, i))
		}
		next = scope(next)
	}
	return next
}
