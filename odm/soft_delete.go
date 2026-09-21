package odm

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// SoftDeletes opts a model into soft deletion. Embed it alongside Model:
//
//	type User struct {
//		odm.Model       `bson:",inline"`
//		odm.SoftDeletes `bson:",inline"`
//
//		Name string `bson:"name"`
//	}
//
// With it, Delete stops removing documents and stamps deleted_at instead,
// and every query on the collection hides the stamped ones until asked
// otherwise (WithTrashed, OnlyTrashed). Restore clears the stamp;
// ForceDelete removes the document for real.
//
// DeletedAt is a pointer with omitempty, so a live document carries no
// deleted_at field at all rather than a null one. Both shapes read as "not
// deleted" — MongoDB's {deleted_at: null} matches a missing field too — so a
// collection that gains soft deletes later doesn't need backfilling.
type SoftDeletes struct {
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// softDeletable marks a model that embeds SoftDeletes, so a query knows to
// apply the default scope without an instance to inspect. Only
// *SoftDeletes implements it.
type softDeletable interface {
	isSoftDeletable()
}

func (s *SoftDeletes) isSoftDeletable() {}

// trashedMode is how a query treats soft-deleted documents.
type trashedMode int8

const (
	// trashedExcluded is the default: soft-deleted documents are invisible.
	trashedExcluded trashedMode = iota
	trashedIncluded
	trashedOnly
)

// softDeleteFilter is the fragment a mode contributes, or nil for none.
// Applied at compile time rather than when the query is built, so
// WithTrashed overrides the default whatever order the chain is written in.
func softDeleteFilter(mode trashedMode) bson.M {
	switch mode {
	case trashedExcluded:
		return bson.M{deletedAtField: nil}
	case trashedOnly:
		return bson.M{deletedAtField: bson.M{"$ne": nil}}
	default:
		return nil
	}
}

// WithTrashed includes soft-deleted documents alongside live ones.
func (q *Query[T]) WithTrashed() *Query[T] {
	return q.withTrashedMode("WithTrashed", trashedIncluded)
}

// OnlyTrashed narrows the query to soft-deleted documents alone.
func (q *Query[T]) OnlyTrashed() *Query[T] {
	return q.withTrashedMode("OnlyTrashed", trashedOnly)
}

func (q *Query[T]) withTrashedMode(call string, mode trashedMode) *Query[T] {
	if err := q.requireSoftDeletes(call); err != nil {
		return q.withError(err)
	}

	next := *q
	next.trashed = mode
	return &next
}

// requireSoftDeletes rejects a soft-delete-only call on a model that doesn't
// embed SoftDeletes, where it could otherwise filter or stamp a field the
// model has no place to store.
func (q *Query[T]) requireSoftDeletes(call string) error {
	if q.collection.meta.softDeletes {
		return nil
	}
	return fmt.Errorf("%w: %s: the model does not embed odm.SoftDeletes", ErrInvalidQuery, call)
}

// Restore clears deleted_at on the soft-deleted documents the filter
// matches, bringing them back into normal queries, and refreshes updated_at
// on a timestamped model.
//
// It only ever touches soft-deleted documents: WithTrashed and OnlyTrashed
// make no difference to it, since restoring a live document is a no-op
// either way and scoping to the trashed ones keeps the returned
// ModifiedCount meaningful.
func (q *Query[T]) Restore(ctx context.Context) (*mongo.UpdateResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	if err := q.requireSoftDeletes("Restore"); err != nil {
		return nil, err
	}

	result, err := q.collection.collection.UpdateMany(ctx, q.compileFilter(trashedOnly), compileUpdate(q.restoreOps()))
	return result, classify(err)
}

// restoreOps clears the soft-delete stamp, refreshing updated_at on a
// timestamped model unless the caller opted out.
func (q *Query[T]) restoreOps() []updateOp {
	ops := []updateOp{{operator: "$unset", field: deletedAtField, value: ""}}
	if q.collection.meta.timestamps && !q.skipTimestamps {
		ops = append(ops, updateOp{operator: "$set", field: updatedAtField, value: q.collection.now()})
	}
	return ops
}

// ForceDelete permanently removes the matching documents from a soft-delete
// model, the operation Delete performs for every other model.
//
// It ignores the default scope that hides soft-deleted documents: purging is
// the reason to reach for this, and silently skipping the already-trashed
// ones would be the surprising behavior. Narrow it with OnlyTrashed to purge
// just those, which it does honour.
func (q *Query[T]) ForceDelete(ctx context.Context) (*mongo.DeleteResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	if err := q.requireSoftDeletes("ForceDelete"); err != nil {
		return nil, err
	}

	return q.collection.collection.DeleteMany(ctx, q.compileFilter(forceDeleteMode(q.trashed)))
}

// forceDeleteMode drops the default scope that hides soft-deleted documents,
// while honouring a mode the caller chose deliberately.
func forceDeleteMode(mode trashedMode) trashedMode {
	if mode == trashedExcluded {
		return trashedIncluded
	}
	return mode
}

// softDeleteUpdate is the update Delete and DeleteOne apply in place of a
// removal: stamp deleted_at, and refresh updated_at on a timestamped model.
func (q *Query[T]) softDeleteUpdate() bson.D {
	now := q.collection.now()

	ops := []updateOp{{operator: "$set", field: deletedAtField, value: now}}
	if q.collection.meta.timestamps && !q.skipTimestamps {
		ops = append(ops, updateOp{operator: "$set", field: updatedAtField, value: now})
	}
	return compileUpdate(ops)
}
