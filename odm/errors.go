package odm

import (
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ErrModelNotFound is returned by First (and so by Find) when no document
// matches the query. Callers match it with errors.Is instead of having to
// know about the driver's own mongo.ErrNoDocuments — which stays wrapped
// underneath, so errors.Is(err, mongo.ErrNoDocuments) holds too for code
// that wants the native error.
var ErrModelNotFound = errors.New("odm: model not found")

// ErrInvalidQuery is returned by any terminal method — Get, First, Find,
// Count, Exists, CursorPaginate, Aggregate, Update, UpdateOne, UpdateRaw,
// Delete, DeleteOne, Restore, ForceDelete — when an earlier builder call was
// handed something that can't compile into a query.
//
// Builder methods return *Query[T] so they can chain, which leaves them
// nowhere to put an error. The query records the first failure instead and
// surfaces it at the terminal, where the caller is already checking an
// error. Each is wrapped with the call that caused it; match with errors.Is.
//
// The causes, by the call that records them:
//
//   - Where/OrWhere: an unknown operator, a non-string operator, or the
//     wrong number of arguments.
//   - WhereIn/WhereNotIn: values that aren't a slice or array.
//   - WhereRaw: a nil filter.
//   - OrderBy: a direction that is neither Asc nor Desc.
//   - Limit/Skip: a negative count.
//   - Select/Exclude: an empty field list, or a projection mixing
//     inclusions and exclusions where MongoDB won't.
//   - Scope: a nil scope.
//   - With: a nil relation, or one missing its ForeignKey or Attach.
//   - WithTrashed/OnlyTrashed/Restore/ForceDelete: a model that doesn't
//     embed odm.SoftDeletes.
//   - Set/Unset/Inc/Push/Pull/AddToSet: two operators targeting one field.
//   - Update/UpdateOne: nothing staged to write.
//   - UpdateRaw: a nil update, or one that would discard staged operators.
//   - CursorPaginate: a Limit that isn't positive.
var ErrInvalidQuery = errors.New("odm: invalid query")

// ErrNilModel is returned by Create when model is nil.
var ErrNilModel = errors.New("odm: model is nil")

// ErrInvalidCursor is returned by CursorPaginate when the cursor it was
// handed can't be used: not this package's encoding, from an older build, or
// minted under a different sort than the query now asks for. Cursors travel
// through URLs and clients, so a stale or edited one is an ordinary thing to
// handle, and every one of those cases lands here rather than on its own
// sentinel.
var ErrInvalidCursor = errors.New("odm: invalid cursor")

// ErrDuplicateKey is returned by a write that violated a unique index —
// inserting a second user with an email another already has, most often.
// It is the one server error worth a name of its own: telling a caller
// "that email is taken" is ordinary application logic, not error handling.
//
// The driver's own error stays wrapped underneath, so errors.As still
// reaches a *mongo.WriteException for the constraint's name and the
// offending value.
var ErrDuplicateKey = errors.New("odm: duplicate key")

// classify names the driver errors this package has a stable meaning for,
// and passes everything else through untouched. Wrapping rather than
// replacing: a caller who wants the server's own diagnostics keeps them.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("%w: %w", ErrDuplicateKey, err)
	}
	return err
}
