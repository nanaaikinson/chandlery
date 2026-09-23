package odm

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// findOptions compiles the query's sort, projection, limit and skip into
// driver options. Pure — no I/O, so it is unit-testable on its own.
func (q *Query[T]) findOptions() *options.FindOptionsBuilder {
	opts := options.Find()
	if len(q.sorts) > 0 {
		opts.SetSort(q.sorts)
	}
	if len(q.projection) > 0 {
		opts.SetProjection(q.projection)
	}
	if q.hasLimit {
		opts.SetLimit(q.limit)
	}
	if q.hasSkip {
		opts.SetSkip(q.skip)
	}
	return opts
}

// findOneOptions is findOptions without the limit, which a single-document
// read has no use for.
func (q *Query[T]) findOneOptions() *options.FindOneOptionsBuilder {
	opts := options.FindOne()
	if len(q.sorts) > 0 {
		opts.SetSort(q.sorts)
	}
	if len(q.projection) > 0 {
		opts.SetProjection(q.projection)
	}
	if q.hasSkip {
		opts.SetSkip(q.skip)
	}
	return opts
}

// Get runs the query and decodes every match. The returned slice is empty
// (and may be nil) when nothing matches — that is not an error. Fields left
// out by Select/Exclude decode as their zero value, and any relation named
// by With is loaded onto the results before they are returned.
func (q *Query[T]) Get(ctx context.Context) ([]T, error) {
	models, raws, err := q.fetch(ctx, len(q.with) > 0)
	if err != nil {
		return nil, err
	}
	if err := q.loadRelations(ctx, models, raws); err != nil {
		return nil, err
	}
	return models, nil
}

// fetch runs the query, optionally keeping each document's raw bytes
// alongside the decoded model. The raw bytes are what relation loading and
// cursor pagination read their key values from: the field they need is named
// by a string and may not be a field of T at all, so going back to the bytes
// is both exact and free of reflection.
func (q *Query[T]) fetch(ctx context.Context, keepRaw bool) ([]T, []bson.Raw, error) {
	if q.err != nil {
		return nil, nil, q.err
	}

	cursor, err := q.collection.collection.Find(ctx, q.filter(), q.findOptions())
	if err != nil {
		return nil, nil, err
	}

	if !keepRaw {
		// Cursor.All drains the cursor, closes it even on failure, and
		// reports both iteration and decode errors — so nothing here is
		// swallowed and no cursor is left open.
		var models []T
		if err := cursor.All(ctx, &models); err != nil {
			return nil, nil, err
		}
		if err := snapshotAll(models, q.collection.meta.trackable); err != nil {
			return nil, nil, err
		}
		return models, nil, nil
	}
	defer cursor.Close(ctx)

	var models []T
	var raws []bson.Raw
	for cursor.Next(ctx) {
		var model T
		if err := cursor.Decode(&model); err != nil {
			return nil, nil, err
		}
		models = append(models, model)
		// Cursor.Current is reused between iterations, so each document's
		// bytes have to be copied out.
		raws = append(raws, bson.Raw(append([]byte(nil), cursor.Current...)))
	}
	if err := cursor.Err(); err != nil {
		return nil, nil, err
	}
	if err := snapshotAll(models, q.collection.meta.trackable); err != nil {
		return nil, nil, err
	}
	return models, raws, nil
}

// First returns the first matching document, honouring OrderBy, Skip and any
// projection (Limit is irrelevant to a single-document read). When nothing
// matches it returns the zero T and an error matching both ErrModelNotFound
// and the driver's mongo.ErrNoDocuments.
func (q *Query[T]) First(ctx context.Context) (T, error) {
	var model T
	if q.err != nil {
		return model, q.err
	}

	raw, err := q.collection.collection.FindOne(ctx, q.filter(), q.findOneOptions()).Raw()
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return model, fmt.Errorf("%w: %w", ErrModelNotFound, err)
	case err != nil:
		return model, err
	}
	if err := bson.Unmarshal(raw, &model); err != nil {
		return model, err
	}

	// One model is still one batch: a relation costs one query, not one per
	// parent, whether there is a page of them or a single document.
	models := []T{model}
	if err := snapshotAll(models, q.collection.meta.trackable); err != nil {
		return model, err
	}
	if err := q.loadRelations(ctx, models, []bson.Raw{raw}); err != nil {
		return model, err
	}
	return models[0], nil
}

// Find returns the document whose _id is id, subject to any conditions
// already on the query. id is passed to MongoDB as-is, so it can be a
// bson.ObjectID (what an embedded odm.Model stores), a string, or whatever
// else the model's own _id holds. Missing documents behave as in First.
func (q *Query[T]) Find(ctx context.Context, id any) (T, error) {
	return q.Where("_id", id).First(ctx)
}

// Count reports how many documents match the query's filter. Like
// db.Repository's Count, it ignores Limit and Skip: the useful number
// alongside a page of results is the full total, not the page's own size.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	return q.collection.collection.CountDocuments(ctx, q.filter())
}

// Exists reports whether anything matches, without decoding a document: the
// count stops at the first match server-side.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	if q.err != nil {
		return false, q.err
	}

	count, err := q.collection.collection.CountDocuments(ctx, q.filter(), options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Update applies the staged operators (Set, Unset, Inc, Push, Pull,
// AddToSet) to *every* document matching the filter, and returns the
// driver's own result:
//
//	result, err := users.
//		Where("_id", userID).
//		Set("name", "Nana").
//		Inc("login_count", 1).
//		Update(ctx)
//
// Use UpdateOne for single-document semantics. A query with no staged
// operators is an error, not a no-op.
func (q *Query[T]) Update(ctx context.Context) (*mongo.UpdateResult, error) {
	update, err := q.stagedUpdate("Update")
	if err != nil {
		return nil, err
	}
	result, err := q.collection.collection.UpdateMany(ctx, q.filter(), update)
	return result, classify(err)
}

// UpdateOne is Update against at most one matching document, honouring
// OrderBy to choose which one:
//
//	job, err := jobs.
//		Where("status", "pending").
//		Oldest().
//		Set("status", "processing").
//		UpdateOne(ctx)
//
// The sort goes to the server as part of the update, which is what makes
// this package require MongoDB 8.0: earlier servers reject the field. It is
// one atomic round trip, and MatchedCount and ModifiedCount are the server's
// own.
func (q *Query[T]) UpdateOne(ctx context.Context) (*mongo.UpdateResult, error) {
	update, err := q.stagedUpdate("UpdateOne")
	if err != nil {
		return nil, err
	}
	return q.updateOne(ctx, update)
}

func (q *Query[T]) updateOne(ctx context.Context, update any) (*mongo.UpdateResult, error) {
	collection := q.collection.collection
	if len(q.sorts) == 0 {
		result, err := collection.UpdateOne(ctx, q.filter(), update)
		return result, classify(err)
	}

	result, err := collection.UpdateOne(ctx, q.filter(), update, options.UpdateOne().SetSort(q.sorts))
	return result, classify(err)
}

// stagedUpdate compiles the staged operators, rejecting an empty update
// before it reaches the server.
func (q *Query[T]) stagedUpdate(call string) (bson.D, error) {
	if q.err != nil {
		return nil, q.err
	}
	if len(q.updates) == 0 {
		return nil, fmt.Errorf("%w: %s: no update operators staged — call Set/Unset/Inc/Push/Pull/AddToSet, or use UpdateRaw", ErrInvalidQuery, call)
	}
	return compileUpdate(q.timestampedOps(q.updates)), nil
}

// timestampedOps adds the automatic updated_at refresh to a staged update.
// It stays out of the way when the model has no timestamps, when the caller
// asked for none via WithoutTimestamps, or when the update already writes
// updated_at itself — that last case both respects an explicit value and
// avoids the duplicate-field conflict MongoDB would reject.
func (q *Query[T]) timestampedOps(ops []updateOp) []updateOp {
	if !q.collection.meta.timestamps || q.skipTimestamps {
		return ops
	}
	for _, op := range ops {
		if op.field == updatedAtField {
			return ops
		}
	}
	return cloneAppend(ops, updateOp{operator: "$set", field: updatedAtField, value: q.collection.now()})
}

// UpdateRaw applies a driver-native update document to every matching
// document, for anything the staged operators don't cover — $each, $pop,
// $rename, positional operators, or an aggregation-pipeline update:
//
//	users.Where("_id", id).UpdateRaw(ctx, bson.M{
//		"$set": bson.M{"name": "Nana"},
//		"$inc": bson.M{"login_count": 1},
//	})
//
// update is passed straight through (bson.M, bson.D, or a pipeline), so
// nothing here re-validates MongoDB's update language. It refuses to run
// alongside staged operators rather than silently dropping them; use
// Raw().UpdateOne for the single-document raw case.
func (q *Query[T]) UpdateRaw(ctx context.Context, update any) (*mongo.UpdateResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	if isNil(update) {
		return nil, fmt.Errorf("%w: UpdateRaw: update is nil", ErrInvalidQuery)
	}
	if len(q.updates) > 0 {
		return nil, fmt.Errorf("%w: UpdateRaw: the query already stages %d operator(s) via Set/Inc/... — use Update, or build the whole update raw", ErrInvalidQuery, len(q.updates))
	}
	result, err := q.collection.collection.UpdateMany(ctx, q.filter(), update)
	return result, classify(err)
}

// Delete removes *every* document matching the filter.
//
// On a model embedding odm.SoftDeletes it stamps deleted_at instead of
// removing anything, which hides the documents from later queries until
// Restore clears the stamp; ForceDelete is the physical delete there. On
// every other model it is a physical MongoDB delete, and nothing is
// recoverable afterwards.
//
// DeletedCount counts the documents the call removed from view either way —
// on the soft-delete path it is the underlying update's ModifiedCount, so an
// already-trashed document (which the default scope excludes anyway) doesn't
// count twice.
//
// An unfiltered query hits the whole collection, which is why Delete lives
// on Query and not on Collection — clearing everything takes an explicit
// users.Query().Delete(ctx).
func (q *Query[T]) Delete(ctx context.Context) (*mongo.DeleteResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	if !q.collection.meta.softDeletes {
		return q.collection.collection.DeleteMany(ctx, q.filter())
	}

	result, err := q.collection.collection.UpdateMany(ctx, q.filter(), q.softDeleteUpdate())
	if err != nil {
		return nil, classify(err)
	}
	return &mongo.DeleteResult{DeletedCount: result.ModifiedCount}, nil
}

// DeleteOne removes at most one matching document, soft-deleting it on a
// model that embeds odm.SoftDeletes exactly as Delete does, and honouring
// OrderBy to choose which one.
//
// Unlike UpdateOne, this can't hand the sort to deleteOne: the driver
// exposes no sort option there, whatever the server supports. A sorted
// DeleteOne goes through findAndModify instead, an unsorted one is a plain
// deleteOne, and both are atomic with an exact DeletedCount.
func (q *Query[T]) DeleteOne(ctx context.Context) (*mongo.DeleteResult, error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.collection.meta.softDeletes {
		// updateOne carries the same sort handling a physical DeleteOne
		// gets below, so the soft path picks its document the same way.
		result, err := q.updateOne(ctx, q.softDeleteUpdate())
		if err != nil {
			return nil, err
		}
		return &mongo.DeleteResult{DeletedCount: result.ModifiedCount}, nil
	}
	if len(q.sorts) == 0 {
		return q.collection.collection.DeleteOne(ctx, q.filter())
	}

	opts := options.FindOneAndDelete().
		SetSort(q.sorts).
		SetProjection(bson.M{"_id": 1})

	switch err := q.collection.collection.FindOneAndDelete(ctx, q.filter(), opts).Err(); {
	case errors.Is(err, mongo.ErrNoDocuments):
		return &mongo.DeleteResult{}, nil
	case err != nil:
		return nil, err
	}
	return &mongo.DeleteResult{DeletedCount: 1}, nil
}
