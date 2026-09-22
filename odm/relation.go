package odm

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Relation is one declared link between a model and another collection:
// BelongsTo, HasOne or HasMany. It carries only the parent's type, because
// the related type is captured inside the declaration itself — which is what
// lets relations of different target types sit in one With call.
//
// The interface is closed on purpose: its methods are unexported, so the
// three declarations in this package are the whole set. Many-to-many and
// polymorphic links are not here, and a link this package doesn't model is
// two queries and a loop in your own code, which is all this is.
type Relation[T any] interface {
	// validate reports a declaration that can't work, before any I/O.
	validate() error
	// load fetches the related documents for every parent at once and
	// attaches them. parents and raws are the same documents: the decoded
	// models to attach onto, and the bytes they were decoded from, which is
	// where the key values are read from.
	load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error
}

// With eager-loads relations alongside the query's own results:
//
//	users, err := users.With(UserOrders).Get(ctx)
//
// Each relation costs exactly one extra query, whatever the number of
// parents: the keys are collected and the related documents fetched with a
// single $in. That is the whole point — a relation loaded per parent is the
// N+1 this exists to avoid.
//
// Relations load after the parents are decoded, so they apply to Get, First,
// Find and CursorPaginate alike.
//
// A relation can carry its own, through its With method, and the levels
// stay batched: users, then every order belonging to any of them, then every
// payment belonging to any of those — three queries for three levels,
// whatever the number of rows at each.
//
//	users.With(UserOrders.With(OrderPayments)).Get(ctx)
//
// The related query runs under the related model's own rules, so a
// soft-deleting target hides its trashed documents here too.
func (q *Query[T]) With(relations ...Relation[T]) *Query[T] {
	for i, relation := range relations {
		if relation == nil {
			return q.withError(fmt.Errorf("%w: With: relation %d is nil", ErrInvalidQuery, i))
		}
		if err := relation.validate(); err != nil {
			return q.withError(fmt.Errorf("%w: With: relation %d: %w", ErrInvalidQuery, i, err))
		}
	}

	next := *q
	next.with = cloneAppend(q.with, relations...)
	return &next
}

// loadRelations runs each declared relation over a page of results.
func (q *Query[T]) loadRelations(ctx context.Context, parents []T, raws []bson.Raw) error {
	if len(q.with) == 0 || len(parents) == 0 {
		return nil
	}

	for _, relation := range q.with {
		if err := relation.load(ctx, q.collection.db, parents, raws); err != nil {
			return err
		}
	}
	return nil
}

// keyID is a BSON value reduced to something comparable: its type byte
// followed by its raw bytes. Grouping on that is exact for the key types
// anyone actually uses — a ULID string, an ObjectID, a UUID — and needs no
// reflection and no decoding.
//
// It compares by BSON type as well as bytes, so an int32 parent key and an
// int64 foreign key would not match here even though MongoDB's own $in
// would. Keep a key's type consistent across the two collections, which any
// schema written by one program already does.
type keyID string

func keyOf(value bson.RawValue) keyID {
	return keyID(append([]byte{byte(value.Type)}, value.Value...))
}

// fieldKeys reads one field out of a document and returns the keys it
// contributes: one for a scalar, one per element for an array.
//
// That array case is the whole of many-to-many. MongoDB stores "this user
// has these roles" as an array of ids on the user, so a relation key is a
// list as often as it is a single value, and a relation that can only match
// scalars can only express one side of a graph. Expanding here means every
// declaration handles both without knowing which it has: user.role_ids to
// role._id, role._id back to user.role_ids, or arrays on both sides.
func fieldKeys(raw bson.Raw, field string) ([]bson.RawValue, bool) {
	value, err := raw.LookupErr(strings.Split(field, ".")...)
	if err != nil {
		// A document without the key simply has no related documents.
		return nil, false
	}

	if value.Type != bson.TypeArray {
		return []bson.RawValue{value}, true
	}

	elements, err := value.Array().Values()
	if err != nil {
		return nil, false
	}
	return elements, true
}

// documentKeys reads one field out of every document, returning the keys
// each one contributes (none where the field is absent, several where it
// holds an array) and the distinct values to match against, decoded so they
// can go into an $in.
//
// Reading from the raw bytes rather than the decoded model is what keeps
// this reflection-free: the field is named in the declaration, and the
// bytes are already there.
func documentKeys(raws []bson.Raw, field string) ([][]keyID, bson.A, error) {
	keys := make([][]keyID, len(raws))
	values := make(bson.A, 0, len(raws))
	seen := make(map[keyID]struct{}, len(raws))

	for i, raw := range raws {
		elements, ok := fieldKeys(raw, field)
		if !ok {
			continue
		}

		keys[i] = make([]keyID, 0, len(elements))
		for _, element := range elements {
			key := keyOf(element)
			keys[i] = append(keys[i], key)

			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			var decoded any
			if err := element.Unmarshal(&decoded); err != nil {
				return nil, nil, fmt.Errorf("odm: reading relation key %q: %w", field, err)
			}
			values = append(values, decoded)
		}
	}

	return keys, values, nil
}

// groupByKey indexes documents by one field's value, so attaching is a map
// lookup per parent rather than a scan. A document whose key field holds an
// array is indexed under every element, which is what lets the other side of
// a many-to-many find it.
func groupByKey(raws []bson.Raw, field string) map[keyID][]int {
	grouped := make(map[keyID][]int, len(raws))
	for i, raw := range raws {
		elements, ok := fieldKeys(raw, field)
		if !ok {
			continue
		}
		for _, element := range elements {
			key := keyOf(element)
			grouped[key] = append(grouped[key], i)
		}
	}
	return grouped
}

// matchesFor collects the related documents one parent's keys reach, in the
// order MongoDB returned them and without repeats — a parent holding the
// same id twice, or reaching one document by two of its keys, still gets it
// once.
func matchesFor(keys []keyID, grouped map[keyID][]int) []int {
	if len(keys) == 1 {
		return grouped[keys[0]]
	}

	var matches []int
	seen := map[int]struct{}{}
	for _, key := range keys {
		for _, index := range grouped[key] {
			if _, ok := seen[index]; ok {
				continue
			}
			seen[index] = struct{}{}
			matches = append(matches, index)
		}
	}
	slices.Sort(matches)
	return matches
}

// relatedDocuments fetches everything whose field matches one of values,
// keeping the raw bytes so the results can be grouped by that same field.
func relatedDocuments[R any](ctx context.Context, db *DB, field string, values bson.A) ([]R, []bson.Raw, error) {
	return Use[R](db).WhereIn(field, values).fetch(ctx, true)
}

// loadNested runs a relation's own relations over the documents it just
// fetched, before they are handed to Attach — Attach copies values, so a
// nested relation attached afterwards would land on a copy nobody keeps.
func loadNested[R any](ctx context.Context, db *DB, nested []Relation[R], related []R, raws []bson.Raw) error {
	for _, relation := range nested {
		if err := relation.load(ctx, db, related, raws); err != nil {
			return err
		}
	}
	return nil
}

// validateNested checks a relation's own relations at the same time as the
// relation itself, so a mistake three levels down is still reported before
// any query runs.
func validateNested[R any](kind string, nested []Relation[R]) error {
	for i, relation := range nested {
		if relation == nil {
			return fmt.Errorf("%s: nested relation %d is nil", kind, i)
		}
		if err := relation.validate(); err != nil {
			return fmt.Errorf("%s: nested relation %d: %w", kind, i, err)
		}
	}
	return nil
}

// relationKeys is the shared first half of every relation's load: read the
// keys off the parents, and bail out when there is nothing to match.
func relationKeys(raws []bson.Raw, field string) ([][]keyID, bson.A, bool, error) {
	keys, values, err := documentKeys(raws, field)
	if err != nil {
		return nil, nil, false, err
	}
	return keys, values, len(values) > 0, nil
}
