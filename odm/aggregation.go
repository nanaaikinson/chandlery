package odm

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Aggregate runs a native aggregation pipeline and decodes the results into
// T:
//
//	totals, err := users.
//		Where("status", "completed").
//		Aggregate(ctx, mongo.Pipeline{
//			bson.D{{Key: "$group", Value: bson.M{"_id": "$country", "n": bson.M{"$sum": 1}}}},
//		})
//
// The pipeline is the driver's own mongo.Pipeline and is passed through
// untouched — nothing here wraps MongoDB's aggregation language.
//
// The query's filter is prepended as a $match, so an aggregation is scoped
// the same way every other read on the same query is, soft deletes included.
// That means the first stage the server sees is this package's $match, which
// a pipeline starting with a stage that must come first ($geoNear,
// $changeStream, $indexStats) can't allow — run those through Raw().Aggregate
// instead, which prepends nothing and scopes nothing.
//
// Use AggregateInto when the results aren't shaped like T, which is the
// usual case for a $group.
func (q *Query[T]) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...options.Lister[options.AggregateOptions]) ([]T, error) {
	return aggregate[T, T](ctx, q, pipeline, opts)
}

// AggregateInto is Aggregate decoding into some other shape — a grouped
// total, a projection, a joined document:
//
//	type RevenueByDay struct {
//		Day   string  `bson:"_id"`
//		Total float64 `bson:"total"`
//	}
//
//	rows, err := odm.AggregateInto[Order, RevenueByDay](ctx, orders.Where("status", "paid"), pipeline)
//
// It is a function rather than a method because a method can't introduce a
// type parameter of its own. Context comes first, as everywhere else.
func AggregateInto[T, R any](ctx context.Context, q *Query[T], pipeline mongo.Pipeline, opts ...options.Lister[options.AggregateOptions]) ([]R, error) {
	return aggregate[T, R](ctx, q, pipeline, opts)
}

func aggregate[T, R any](ctx context.Context, q *Query[T], pipeline mongo.Pipeline, opts []options.Lister[options.AggregateOptions]) ([]R, error) {
	if q.err != nil {
		return nil, q.err
	}

	cursor, err := q.collection.collection.Aggregate(ctx, q.scopedPipeline(pipeline), opts...)
	if err != nil {
		return nil, err
	}

	var results []R
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// scopedPipeline prepends the query's filter as a $match, on a new slice so
// the caller's pipeline is never modified. A query with nothing to match on
// contributes no stage at all rather than an empty one.
func (q *Query[T]) scopedPipeline(pipeline mongo.Pipeline) mongo.Pipeline {
	filter := q.filter()
	if match, ok := filter.(bson.M); ok && len(match) == 0 {
		return pipeline
	}

	scoped := make(mongo.Pipeline, 0, len(pipeline)+1)
	scoped = append(scoped, bson.D{{Key: "$match", Value: filter}})
	return append(scoped, pipeline...)
}

// Aggregate runs a pipeline over the whole collection. On a soft-delete
// model it still excludes trashed documents — see Query.Aggregate.
func (c *Collection[T]) Aggregate(ctx context.Context, pipeline mongo.Pipeline, opts ...options.Lister[options.AggregateOptions]) ([]T, error) {
	return c.Query().Aggregate(ctx, pipeline, opts...)
}
