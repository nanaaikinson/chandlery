package odm

import (
	"fmt"
	"reflect"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Operator is a comparison for Where and OrWhere. The constants below are
// the whole set, and they are typed for the same reason Asc and Desc are:
// a misspelled operator should be a compile error, not a runtime one.
//
// A plain string works too — Where("age", ">", 18) — since spelling the
// operator out is often clearer at a call site, and it is the form this
// package shipped with. Both compile to the same filter; only one of them
// catches a typo before the program runs.
type Operator string

const (
	Eq  Operator = "="
	Ne  Operator = "!="
	Gt  Operator = ">"
	Gte Operator = ">="
	Lt  Operator = "<"
	Lte Operator = "<="
)

// eqAlias is the second spelling of equality, accepted because a caller
// reaching for "==" means the same thing and should not have to find out
// otherwise at runtime.
const eqAlias Operator = "=="

// comparisonOperators maps the operators Where accepts to their MongoDB
// equivalents. Equality is handled separately: it compiles to a plain
// {field: value} rather than {field: {$eq: value}}, which is the same query
// and the form anyone reading the shell would have written by hand.
var comparisonOperators = map[Operator]string{
	Ne:  "$ne",
	Gt:  "$gt",
	Gte: "$gte",
	Lt:  "$lt",
	Lte: "$lte",
}

// comparison compiles one Where/OrWhere call's arguments into a filter
// fragment. Two shapes are accepted, and nothing else:
//
//	(field, value)            equality
//	(field, operator, value)  an odm.Operator, or the string spelling it
//
// A wrong argument count or an unknown operator is an error, never a guess:
// an operator this package doesn't recognise could only be compiled into a
// filter that quietly means something else.
func comparison(call, field string, args []any) (bson.M, error) {
	switch len(args) {
	case 1:
		return bson.M{field: args[0]}, nil

	case 2:
		operator, ok := operatorOf(args[0])
		if !ok {
			return nil, fmt.Errorf("%w: %s(%q, ...): operator must be an odm.Operator or a string, got %T", ErrInvalidQuery, call, field, args[0])
		}
		return operatorFilter(call, field, operator, args[1])

	default:
		return nil, fmt.Errorf("%w: %s(%q, ...): want (field, value) or (field, operator, value), got %d argument(s) after the field", ErrInvalidQuery, call, field, len(args))
	}
}

// operatorOf accepts either spelling of an operator.
func operatorOf(arg any) (Operator, bool) {
	switch typed := arg.(type) {
	case Operator:
		return typed, true
	case string:
		return Operator(typed), true
	default:
		return "", false
	}
}

func operatorFilter(call, field string, operator Operator, value any) (bson.M, error) {
	if operator == Eq || operator == eqAlias {
		return bson.M{field: value}, nil
	}
	if mongoOperator, ok := comparisonOperators[operator]; ok {
		return bson.M{field: bson.M{mongoOperator: value}}, nil
	}
	return nil, fmt.Errorf("%w: %s(%q, %q, ...): unknown operator, want one of =, ==, !=, >, >=, <, <=", ErrInvalidQuery, call, field, operator)
}

// buildFilter combines a query's accumulated fragments into one filter
// document.
//
// Fragments are kept as a list rather than merged into a single bson.M as
// they arrive, because a map can only hold one entry per field: merging
// would make Where("age", ">=", 18).Where("age", "<=", 65) silently drop the
// first condition, and would have no place to put a second WhereRaw that
// also uses $or. Wrapping in $and instead keeps every condition, in the
// order it was added, with MongoDB's own AND semantics — which is also what
// makes a raw filter compose with a plain Where rather than replace it.
//
// One fragment needs no wrapper, so the common single-condition query still
// sends the filter a caller would have written by hand.
func buildFilter(fragments []any) any {
	switch len(fragments) {
	case 0:
		return bson.M{}
	case 1:
		return fragments[0]
	default:
		and := make(bson.A, len(fragments))
		copy(and, fragments)
		return bson.M{"$and": and}
	}
}

// bsonArray copies a slice or array of any element type into a bson.A,
// reporting false for anything else. This is the package's only reflection
// over caller-supplied values: a method can't introduce its own type
// parameter, so WhereIn/WhereNotIn take an any and have to check the one
// thing MongoDB requires of it. Copying (rather than passing the caller's
// slice through) keeps the compiled filter stable if they reuse the slice,
// and gives a single element type for tests to assert on.
func bsonArray(values any) (bson.A, bool) {
	value := reflect.ValueOf(values)
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		array := make(bson.A, value.Len())
		for i := range array {
			array[i] = value.Index(i).Interface()
		}
		return array, true
	default:
		return nil, false
	}
}

// isNil reports whether value is nil, including a typed nil hiding inside a
// non-nil interface — a plain "filter == nil" misses a nil bson.M, which
// would otherwise reach the driver as an empty filter matching everything.
func isNil(value any) bool {
	if value == nil {
		return true
	}

	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
