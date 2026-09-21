package odm

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Direction is a sort direction. The values are MongoDB's own (1 and -1), so
// they go straight into a sort document.
type Direction int

const (
	Asc  Direction = 1
	Desc Direction = -1
)

// timestampField is the field Latest and Oldest sort on when given none —
// the one odm.Model provides.
const timestampField = "created_at"

// projectionMode records which kind of projection a query is building, so
// an illegal mixture is caught here rather than by the server.
type projectionMode int8

const (
	projectionNone projectionMode = iota
	projectionInclude
	projectionExclude
)

// Query is an immutable, chainable set of conditions over a Collection[T].
// Every builder method returns a new Query with the addition applied and
// leaves the receiver untouched, so a partially built query is safe to keep
// and branch from:
//
//	base := users.Where("active", true)
//	admins := base.Where("role", "admin")   // active AND admin
//	members := base.Where("role", "member") // active AND member
//
// That holds for update operators too: a Set or Inc on a branch is invisible
// to its parent. The same property makes a Query safe to share across
// goroutines — nothing mutates one after it is built.
//
// A Query holds no cursor or connection, and is only sent to the server by a
// terminal method (Get, First, Find, Count, Exists, Update, UpdateOne,
// UpdateRaw, Delete, DeleteOne).
type Query[T any] struct {
	collection *Collection[T]

	filters  []any
	sorts    bson.D
	limit    int64
	hasLimit bool
	skip     int64
	hasSkip  bool

	projection     bson.D
	projectionMode projectionMode

	updates []updateOp

	// err records the first invalid builder call, surfaced by whichever
	// terminal method runs next. See ErrInvalidQuery.
	err error
}

// cloneAppend returns a copy of s with extra appended, always on a fresh
// backing array. Copy-on-write depends on this exactness: slices.Clone (or a
// plain append) may leave spare capacity, and two queries branched off one
// parent would then append into the same array and overwrite each other's
// last entry. Only the slice actually being extended is copied — the others
// are shared, which is safe precisely because every append goes through here
// and so never writes into shared storage.
func cloneAppend[S ~[]E, E any](s S, extra ...E) S {
	out := make(S, 0, len(s)+len(extra))
	out = append(out, s...)
	return append(out, extra...)
}

// withFilter returns a copy of q with one more filter fragment ANDed on.
func (q *Query[T]) withFilter(fragment any) *Query[T] {
	next := *q
	next.filters = cloneAppend(q.filters, fragment)
	return &next
}

// withError returns a copy of q carrying err, keeping whichever error was
// recorded first: the earliest invalid call is the one worth reporting, and
// a later one is often a consequence of it.
func (q *Query[T]) withError(err error) *Query[T] {
	next := *q
	if next.err == nil {
		next.err = err
	}
	return &next
}

// filter renders the accumulated conditions as one filter document.
func (q *Query[T]) filter() any {
	return buildFilter(q.filters)
}

// Where adds a condition on field, in either of two shapes:
//
//	users.Where("email", email)      // equality
//	users.Where("age", ">=", 18)     // comparison
//
// Operators are =, ==, !=, >, >=, <, and <=. Anything else records
// ErrInvalidQuery rather than compiling into a filter that quietly means
// something different.
//
// Conditions AND together, including two on the same field:
// Where("age", ">=", 18).Where("age", "<=", 65) keeps both.
func (q *Query[T]) Where(field string, args ...any) *Query[T] {
	fragment, err := comparison("Where", field, args)
	if err != nil {
		return q.withError(err)
	}
	return q.withFilter(fragment)
}

// OrWhere ORs its condition with everything accumulated before it. The
// chain is folded strictly left to right, with no operator precedence:
//
//	Where(a).OrWhere(b)            // a OR b
//	Where(a).Where(b).OrWhere(c)   // (a AND b) OR c
//	Where(a).OrWhere(b).Where(c)   // (a OR b) AND c
//
// Read it as "everything so far, OR this". Note the third line: that is not
// SQL's precedence, where AND would bind tighter and give a OR (b AND c).
// Chaining can only produce filters of that left-nested shape — a grouped
// predicate (a AND (b OR c)) needs WhereRaw with an explicit $or, and a
// callback-based grouping DSL is deliberately left to a later phase.
//
// As the first condition in a chain there is nothing to OR with, so OrWhere
// behaves exactly like Where rather than ORing against a match-everything
// filter.
func (q *Query[T]) OrWhere(field string, args ...any) *Query[T] {
	fragment, err := comparison("OrWhere", field, args)
	if err != nil {
		return q.withError(err)
	}
	if len(q.filters) == 0 {
		return q.withFilter(fragment)
	}

	next := *q
	// A fresh one-element slice, so neither operand aliases q's own.
	next.filters = []any{bson.M{"$or": bson.A{buildFilter(q.filters), fragment}}}
	return &next
}

// WhereIn matches documents whose field is any of values, which must be a
// slice or array of any element type:
//
//	users.WhereIn("status", []string{"active", "pending"})
//
// An empty slice is passed through as MongoDB's own $in: [], which matches
// nothing. A non-slice records ErrInvalidQuery rather than shipping a filter
// that would quietly match nothing.
func (q *Query[T]) WhereIn(field string, values any) *Query[T] {
	return q.whereSet("WhereIn", "$in", field, values)
}

// WhereNotIn matches documents whose field is none of values.
//
// Watch the empty case: MongoDB's $nin: [] matches *everything*, including
// documents with no such field — the mirror of $in: [] matching nothing.
// That native behavior is passed through rather than special-cased, so guard
// an empty slice yourself if "exclude nothing" isn't what you want.
func (q *Query[T]) WhereNotIn(field string, values any) *Query[T] {
	return q.whereSet("WhereNotIn", "$nin", field, values)
}

func (q *Query[T]) whereSet(call, operator, field string, values any) *Query[T] {
	array, ok := bsonArray(values)
	if !ok {
		return q.withError(fmt.Errorf("%w: %s(%q, ...): values must be a slice or array, got %T", ErrInvalidQuery, call, field, values))
	}
	return q.withFilter(bson.M{field: bson.M{operator: array}})
}

// WhereNull matches documents where field is null *or absent* — MongoDB
// treats {field: null} as matching both, and this passes that through rather
// than inventing SQL's NULL. For the stricter "present and explicitly null",
// use the native form: WhereRaw(bson.M{field: bson.M{"$type": "null"}}).
func (q *Query[T]) WhereNull(field string) *Query[T] {
	return q.withFilter(bson.M{field: nil})
}

// WhereNotNull matches documents where field is present and not null — the
// exact complement of WhereNull.
func (q *Query[T]) WhereNotNull(field string) *Query[T] {
	return q.withFilter(bson.M{field: bson.M{"$ne": nil}})
}

// WhereBetween matches documents whose field is between from and to,
// inclusive at both ends ($gte/$lte). MongoDB's own comparison rules apply,
// so a document whose field is of another type doesn't match.
func (q *Query[T]) WhereBetween(field string, from, to any) *Query[T] {
	return q.withFilter(bson.M{field: bson.M{"$gte": from, "$lte": to}})
}

// WhereNotBetween matches documents whose field is below from or above to,
// compiled as an explicit $or of $lt and $gt rather than a $not around the
// range.
//
// The two are not the same query: $not also matches documents that have no
// such field at all (they fail the inner condition, so its negation holds),
// while this form requires a field that is actually present and comparable.
// Requiring the field is the less surprising half of that choice, and the
// one that mirrors WhereBetween. For the other reading, use WhereRaw.
func (q *Query[T]) WhereNotBetween(field string, from, to any) *Query[T] {
	return q.withFilter(bson.M{"$or": bson.A{
		bson.M{field: bson.M{"$lt": from}},
		bson.M{field: bson.M{"$gt": to}},
	}})
}

// WhereRaw adds a driver-native filter, for anything the builder doesn't
// express:
//
//	users.
//		Where("is_active", true).
//		WhereRaw(bson.M{"$or": bson.A{
//			bson.M{"email": email},
//			bson.M{"phone": phone},
//		}})
//
// It composes with the rest of the query under AND — it never replaces the
// conditions around it. The filter is held by reference and used as given,
// so don't mutate a map after handing it over.
func (q *Query[T]) WhereRaw(filter any) *Query[T] {
	if isNil(filter) {
		return q.withError(fmt.Errorf("%w: WhereRaw: filter is nil", ErrInvalidQuery))
	}
	return q.withFilter(filter)
}

// Select limits the returned documents to the named fields (an inclusion
// projection). Repeated calls accumulate, and _id comes back unless it is
// explicitly excluded:
//
//	users.Select("name", "email").Exclude("_id")
//
// Fields left out decode as their zero value, so a projected model is a
// partial one.
//
// MongoDB doesn't allow inclusions and exclusions in the same projection
// (bar that _id), so mixing Select with a non-_id Exclude records
// ErrInvalidQuery here rather than failing at the server.
func (q *Query[T]) Select(fields ...string) *Query[T] {
	if len(fields) == 0 {
		return q.withError(fmt.Errorf("%w: Select: needs at least one field", ErrInvalidQuery))
	}

	next := q
	for _, field := range fields {
		if next.projectionMode == projectionExclude {
			return q.withError(fmt.Errorf("%w: Select(%q): cannot add an inclusion to an exclusion projection", ErrInvalidQuery, field))
		}

		projected, err := next.withProjection(field, 1, projectionInclude)
		if err != nil {
			return q.withError(err)
		}
		next = projected
	}
	return next
}

// Exclude drops the named fields from the returned documents (an exclusion
// projection), leaving every other field in place.
//
// Excluding _id is the one thing MongoDB allows alongside an inclusion
// projection, so Select("name").Exclude("_id") is legal while excluding any
// other field there records ErrInvalidQuery.
func (q *Query[T]) Exclude(fields ...string) *Query[T] {
	if len(fields) == 0 {
		return q.withError(fmt.Errorf("%w: Exclude: needs at least one field", ErrInvalidQuery))
	}

	next := q
	for _, field := range fields {
		// Excluding _id never commits the projection to either kind: on its
		// own it is a plain exclusion, inside an inclusion projection it is
		// the exception MongoDB permits, and either way a later Select is
		// still legal.
		mode := next.projectionMode
		if field != "_id" {
			if mode == projectionInclude {
				return q.withError(fmt.Errorf("%w: Exclude(%q): cannot exclude a field other than _id from an inclusion projection", ErrInvalidQuery, field))
			}
			mode = projectionExclude
		}

		projected, err := next.withProjection(field, 0, mode)
		if err != nil {
			return q.withError(err)
		}
		next = projected
	}
	return next
}

// withProjection returns a copy of q with one projection entry added and its
// mode set. An exact repeat adds nothing (a projection document can't carry
// the same key twice) and a contradicting one is an error — a field can't be
// both kept and dropped.
func (q *Query[T]) withProjection(field string, value int, mode projectionMode) (*Query[T], error) {
	next := *q
	next.projectionMode = mode

	for _, existing := range q.projection {
		if existing.Key != field {
			continue
		}
		if existing.Value == value {
			return &next, nil
		}
		return nil, fmt.Errorf("%w: projection: %q is already %s", ErrInvalidQuery, field, projectionVerb(existing.Value))
	}

	next.projection = cloneAppend(q.projection, bson.E{Key: field, Value: value})
	return &next, nil
}

func projectionVerb(value any) string {
	if value == 1 {
		return "included"
	}
	return "excluded"
}

// OrderBy sorts by field. Repeated calls keep their order, so the first is
// the primary sort and later ones break its ties.
func (q *Query[T]) OrderBy(field string, direction Direction) *Query[T] {
	if direction != Asc && direction != Desc {
		return q.withError(fmt.Errorf("%w: OrderBy(%q, %d): direction must be odm.Asc or odm.Desc", ErrInvalidQuery, field, direction))
	}

	next := *q
	next.sorts = cloneAppend(q.sorts, bson.E{Key: field, Value: int(direction)})
	return &next
}

// Latest sorts newest-first. With no argument it sorts on created_at, which
// assumes the model embeds odm.Model (or otherwise has that field) — name
// the field explicitly, Latest("published_at"), when it doesn't.
func (q *Query[T]) Latest(fields ...string) *Query[T] {
	return q.orderByEach(Desc, fields)
}

// Oldest sorts oldest-first, on created_at by default. See Latest.
func (q *Query[T]) Oldest(fields ...string) *Query[T] {
	return q.orderByEach(Asc, fields)
}

func (q *Query[T]) orderByEach(direction Direction, fields []string) *Query[T] {
	if len(fields) == 0 {
		return q.OrderBy(timestampField, direction)
	}

	next := q
	for _, field := range fields {
		next = next.OrderBy(field, direction)
	}
	return next
}

// Limit caps how many documents Get returns. A negative n records
// ErrInvalidQuery; n == 0 is MongoDB's own "no limit", passed through as-is.
// First ignores the limit — it never reads more than one document anyway.
func (q *Query[T]) Limit(n int64) *Query[T] {
	if n < 0 {
		return q.withError(fmt.Errorf("%w: Limit(%d): must not be negative", ErrInvalidQuery, n))
	}

	next := *q
	next.limit, next.hasLimit = n, true
	return &next
}

// Skip discards the first n matching documents. A negative n records
// ErrInvalidQuery.
func (q *Query[T]) Skip(n int64) *Query[T] {
	if n < 0 {
		return q.withError(fmt.Errorf("%w: Skip(%d): must not be negative", ErrInvalidQuery, n))
	}

	next := *q
	next.skip, next.hasSkip = n, true
	return &next
}
