package odm

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Index declares one index on a model's collection. It is a thin
// description that compiles to the driver's own mongo.IndexModel — the
// handful of options most declarations use, not a re-specification of
// MongoDB's index system. Anything beyond these (collation, wildcards, text
// or geo options) belongs on Raw().Indexes(), which this never gets in the
// way of.
type Index struct {
	// Keys is the index specification, ordered: bson.D{{Key: "email", Value: 1}}.
	// Order matters to a compound index, which is why this is a bson.D and
	// not a map. Required.
	Keys bson.D
	// Name overrides MongoDB's generated name ("email_1", "business_id_1_created_at_-1").
	Name string
	// Unique rejects a second document with the same key. A write that
	// violates it surfaces as ErrDuplicateKey.
	Unique bool
	// Sparse leaves out documents that lack the indexed field.
	Sparse bool
	// ExpireAfter turns this into a TTL index, deleting a document that
	// long after the indexed date field. Zero means no expiry. MongoDB's
	// resolution here is whole seconds.
	ExpireAfter time.Duration
	// PartialFilter indexes only the documents matching it, e.g.
	// bson.M{"deleted_at": nil} to keep a unique index off soft-deleted rows.
	PartialFilter any
}

// Indexer lets a model declare its indexes:
//
//	func (User) Indexes() []odm.Index {
//		return []odm.Index{
//			{Keys: bson.D{{Key: "email", Value: 1}}, Unique: true},
//			{Keys: bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}}},
//		}
//	}
//
// Either a value or a pointer receiver works.
type Indexer interface {
	Indexes() []Index
}

// model compiles the declaration into the driver's index model.
func (i Index) model() (mongo.IndexModel, error) {
	if len(i.Keys) == 0 {
		return mongo.IndexModel{}, fmt.Errorf("odm: index has no keys")
	}
	if i.ExpireAfter < 0 {
		return mongo.IndexModel{}, fmt.Errorf("odm: index %v: ExpireAfter is %s, must not be negative", i.Keys, i.ExpireAfter)
	}

	opts := options.Index()
	if i.Name != "" {
		opts.SetName(i.Name)
	}
	if i.Unique {
		opts.SetUnique(true)
	}
	if i.Sparse {
		opts.SetSparse(true)
	}
	if i.ExpireAfter > 0 {
		opts.SetExpireAfterSeconds(int32(i.ExpireAfter.Seconds()))
	}
	if i.PartialFilter != nil {
		opts.SetPartialFilterExpression(i.PartialFilter)
	}

	return mongo.IndexModel{Keys: i.Keys, Options: opts}, nil
}

// indexModels compiles a whole declaration, naming the offender if one of
// them is malformed.
func indexModels(indexes []Index) ([]mongo.IndexModel, error) {
	models := make([]mongo.IndexModel, len(indexes))
	for i, index := range indexes {
		model, err := index.model()
		if err != nil {
			return nil, fmt.Errorf("odm: SyncIndexes: index %d: %w", i, err)
		}
		models[i] = model
	}
	return models, nil
}

// SyncIndexes creates the indexes T declares through Indexer, and is safe to
// call on every start: creating an index that already exists with the same
// specification does nothing.
//
// It only ever adds. An index that is no longer declared is left in place,
// and one whose declaration changed reports MongoDB's own conflict error
// rather than being quietly rebuilt — dropping an index is the kind of thing
// that should be a deliberate act against Raw().Indexes(), not a side effect
// of a deploy.
//
// A model that declares no indexes is an error rather than a silent no-op:
// calling this on one is a mistake worth hearing about.
func (c *Collection[T]) SyncIndexes(ctx context.Context) error {
	var zero T
	indexer, ok := any(&zero).(Indexer)
	if !ok {
		if indexer, ok = any(zero).(Indexer); !ok {
			return fmt.Errorf("odm: SyncIndexes: %T declares no indexes — implement odm.Indexer", zero)
		}
	}

	indexes := indexer.Indexes()
	if len(indexes) == 0 {
		return nil
	}

	models, err := indexModels(indexes)
	if err != nil {
		return err
	}

	_, err = c.collection.Indexes().CreateMany(ctx, models)
	return err
}
