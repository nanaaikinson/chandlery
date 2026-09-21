package odm

import "errors"

// ErrModelNotFound is returned by First (and so by Find) when no document
// matches the query. Callers match it with errors.Is instead of having to
// know about the driver's own mongo.ErrNoDocuments — which stays wrapped
// underneath, so errors.Is(err, mongo.ErrNoDocuments) holds too for code
// that wants the native error.
var ErrModelNotFound = errors.New("odm: model not found")

// ErrInvalidQuery is returned by a terminal method (Get, First, Find, Count,
// Exists, Update, UpdateOne, UpdateRaw, Delete, DeleteOne) when an earlier
// builder call was handed invalid input: a negative Limit or Skip, an
// unknown Where operator, an OrderBy with a direction that is neither Asc
// nor Desc, a WhereIn whose values aren't a slice, a nil WhereRaw filter, an
// illegal projection, two update operators targeting one field. It also
// covers an update with nothing staged and an UpdateRaw that would discard
// staged operators. Builder
// methods return *Query[T] so they can chain and have nowhere to put an
// error, so the query records the first failure and surfaces it here, where
// the caller is already checking an error. Wrapped with the offending call
// for context; match with errors.Is.
var ErrInvalidQuery = errors.New("odm: invalid query")

// ErrNilModel is returned by Create when model is nil.
var ErrNilModel = errors.New("odm: model is nil")
