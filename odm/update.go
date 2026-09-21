package odm

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// updateOp is one staged update operation. Operations are kept as a flat,
// ordered list and grouped into a MongoDB update document only at execution
// (see compileUpdate) — the same reason filters are kept as fragments:
// grouping eagerly into a map would lose both the call order and any chance
// of reporting a conflict against the call that caused it.
type updateOp struct {
	operator string // "$set", "$inc", ...
	field    string
	value    any
}

// Set stages $set on field.
//
//	users.Where("_id", id).Set("name", "Nana").Update(ctx)
//
// Dotted paths reach into embedded documents ("profile.address.country") the
// same way they do in a filter.
func (q *Query[T]) Set(field string, value any) *Query[T] {
	return q.withUpdate("Set", "$set", field, value)
}

// Unset stages $unset on field, removing it from the document entirely —
// which is not the same as setting it to null (see WhereNull).
func (q *Query[T]) Unset(field string) *Query[T] {
	// MongoDB ignores $unset's value; "" is the conventional placeholder.
	return q.withUpdate("Unset", "$unset", field, "")
}

// Inc stages $inc on field, atomically adding value (negative to subtract).
// value must be a number — MongoDB rejects anything else, and this doesn't
// duplicate that check. An absent field starts from zero.
func (q *Query[T]) Inc(field string, value any) *Query[T] {
	return q.withUpdate("Inc", "$inc", field, value)
}

// Increment is Inc with an int64, for the common counter case.
func (q *Query[T]) Increment(field string, by int64) *Query[T] {
	return q.Inc(field, by)
}

// Decrement is Inc with a negated int64, so a caller counting down doesn't
// have to get the sign right.
func (q *Query[T]) Decrement(field string, by int64) *Query[T] {
	return q.Inc(field, -by)
}

// Push stages $push, appending value to an array field (creating the array
// if the field is absent). One Push appends one element; to append several
// in a single operation use $each through UpdateRaw.
func (q *Query[T]) Push(field string, value any) *Query[T] {
	return q.withUpdate("Push", "$push", field, value)
}

// Pull stages $pull, removing every element of the array at field that
// matches value. A plain value removes equal elements; a bson.M is treated
// by MongoDB as a condition ($pull: {tags: {$in: [...]}}).
func (q *Query[T]) Pull(field string, value any) *Query[T] {
	return q.withUpdate("Pull", "$pull", field, value)
}

// AddToSet stages $addToSet, appending value to an array field only if it
// isn't already there.
func (q *Query[T]) AddToSet(field string, value any) *Query[T] {
	return q.withUpdate("AddToSet", "$addToSet", field, value)
}

// withUpdate returns a copy of q with one more staged operation, refusing a
// second operation on the same field.
//
// MongoDB rejects an update whose operators target the same path twice
// ("would create a conflict at ..."), whether that's $set and $inc on one
// field or two $push calls on one array. Catching it here names the call
// that caused it, instead of surfacing a server error at execution.
func (q *Query[T]) withUpdate(call, operator, field string, value any) *Query[T] {
	for _, existing := range q.updates {
		if existing.field != field {
			continue
		}
		return q.withError(fmt.Errorf("%w: %s(%q, ...): %q is already targeted by %s in this update — MongoDB allows a field only once per update; use UpdateRaw for anything that needs $each or a single combined operator", ErrInvalidQuery, call, field, field, existing.operator))
	}

	next := *q
	next.updates = cloneAppend(q.updates, updateOp{operator: operator, field: field, value: value})
	return &next
}

// compileUpdate groups the staged operations into a MongoDB update document,
// as a bson.D so the output is ordered and so tests can assert on it
// structurally: operators appear in the order they were first used, and
// fields in the order they were staged.
func compileUpdate(ops []updateOp) bson.D {
	update := bson.D{}
	for _, op := range ops {
		entry := bson.E{Key: op.field, Value: op.value}

		index := -1
		for i := range update {
			if update[i].Key == op.operator {
				index = i
				break
			}
		}
		if index < 0 {
			update = append(update, bson.E{Key: op.operator, Value: bson.D{entry}})
			continue
		}
		update[index].Value = append(update[index].Value.(bson.D), entry)
	}
	return update
}
