package odm

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// BulkWrite hands a batch of native write models straight to the driver:
//
//	result, err := users.BulkWrite(ctx, []mongo.WriteModel{
//		mongo.NewUpdateOneModel().
//			SetFilter(bson.M{"_id": id}).
//			SetUpdate(bson.M{"$set": bson.M{"status": "active"}}),
//		mongo.NewDeleteOneModel().SetFilter(bson.M{"_id": other}),
//	})
//
// Nothing is rewritten on the way through: no soft-delete scope is applied,
// no timestamp is stamped, no hook runs. A bulk write is a batch of
// instructions the caller composed, and second-guessing them is how a
// bulk API stops being usable for what bulk APIs are for. Build the filters
// and updates the same way you would for the driver.
func (c *Collection[T]) BulkWrite(ctx context.Context, models []mongo.WriteModel, opts ...options.Lister[options.BulkWriteOptions]) (*mongo.BulkWriteResult, error) {
	result, err := c.collection.BulkWrite(ctx, models, opts...)
	return result, classify(err)
}

// CreateMany inserts several models in one round trip, with the same
// preparation Create gives one: BeforeCreate on each, then the ObjectID and
// timestamps, then a single insert, then AfterCreate on each. Every model is
// prepared before anything is sent, so a BeforeCreate failure means nothing
// was written.
//
// The insert is MongoDB's own InsertMany, which is not a transaction: by
// default it stops at the first failing document and the ones before it stay
// written. Pass options.InsertMany().SetOrdered(false) to keep going instead,
// or run it inside a session when all-or-nothing matters.
//
// Models are taken as pointers so the assigned IDs and timestamps land on
// the caller's own values, exactly as Create does.
func (c *Collection[T]) CreateMany(ctx context.Context, models []*T, opts ...options.Lister[options.InsertManyOptions]) error {
	if len(models) == 0 {
		return nil
	}

	documents := make([]any, len(models))
	for i, model := range models {
		if model == nil {
			return ErrNilModel
		}
		if err := runBeforeCreate(ctx, model); err != nil {
			return err
		}
		if err := notify(ctx, c.db, eventCreating, model); err != nil {
			return err
		}
		if hook, ok := any(model).(inserter); ok {
			hook.prepareForInsert(c.now())
		}
		documents[i] = model
	}

	if _, err := c.collection.InsertMany(ctx, documents, opts...); err != nil {
		return classify(err)
	}

	for _, model := range models {
		if err := snapshot(model); err != nil {
			return err
		}
		if err := runAfterCreate(ctx, model); err != nil {
			return err
		}
		if err := notify(ctx, c.db, eventCreated, model); err != nil {
			return err
		}
	}
	return nil
}
