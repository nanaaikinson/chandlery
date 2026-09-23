// Package odm is a MongoDB object-document mapper: a generic, copy-on-write
// query builder (odm.Query[T]) over a typed collection (odm.Collection[T]),
// shaped after Laravel Eloquent's developer experience but staying native
// MongoDB underneath.
//
// It requires MongoDB 8.0 or later: a sorted UpdateOne hands its sort to the
// server, which earlier versions reject.
//
//	database := odm.New(client.Database("app"))
//	users := odm.Use[User](database)
//
//	user, err := users.
//		Where("email", "nana@example.com").
//		Where("is_active", true).
//		First(ctx)
//
// Filters are plain bson, sorts are plain bson, updates compile to MongoDB's
// own operators, and every layer hands back the driver's own type — DB.Raw,
// Collection.Raw, Query.WhereRaw, Query.UpdateRaw, Query.Aggregate — so
// nothing here can trap a caller who needs something the builder doesn't
// cover.
//
// Beyond querying, a model can opt into behavior by embedding:
//
//   - odm.IdentityModel gives an ObjectID _id, and odm.Model adds
//     created_at/updated_at that Create stamps and Update refreshes.
//   - odm.SoftDeletes turns Delete into a deleted_at stamp that later reads
//     skip, with WithTrashed, OnlyTrashed, Restore and ForceDelete to reach
//     past it.
//
// A model that embeds one of the first two also remembers the document it
// came from, which is what Save diffs against to write only what changed.
// BeforeCreate, AfterCreate, BeforeUpdate and AfterUpdate hook the model's
// own lifecycle; Observe registers the same events outside the model type.
//
// Relations (odm.HasMany, odm.HasOne, odm.BelongsTo, odm.BelongsToMany) are
// declared explicitly and loaded by Query.With, batched into one query each —
// including many-to-many, which MongoDB stores as a list of ids rather than
// a join collection. Query.CursorPaginate
// pages by seeking rather than skipping. DB.Transaction runs a callback
// inside a MongoDB transaction, and Collection.SyncIndexes creates whatever
// a model declares through odm.Indexer.
//
// It is deliberately separate from this repo's db package: the two share no
// types and never import each other.
package odm

import (
	"reflect"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DB is a handle to one Mongo database. It holds no connection of its own —
// the *mongo.Client behind it, and closing that client, stays the caller's
// business.
type DB struct {
	database *mongo.Database
	now      func() time.Time

	// observers are registered per database rather than per process, so
	// nothing here is package-global mutable state. See Observe.
	observerMu sync.RWMutex
	observers  map[reflect.Type][]any
}

// Option configures a DB.
type Option func(*DB)

// WithClock overrides the time source Create stamps an embedded Model's
// CreatedAt/UpdatedAt from, so a test can assert on a fixed instant instead
// of whatever time.Now happened to return.
func WithClock(now func() time.Time) Option {
	return func(db *DB) { db.now = now }
}

// New wraps an already-connected database. It neither creates nor connects a
// mongo.Client: the caller owns that lifecycle, and odm never closes a
// connection it didn't open.
//
// Panics on a nil database — a wiring mistake worth failing at startup
// rather than on the first query hours later.
func New(database *mongo.Database, opts ...Option) *DB {
	if database == nil {
		panic("odm: New: database is nil")
	}

	db := &DB{database: database, now: time.Now}
	for _, opt := range opts {
		opt(db)
	}
	return db
}

// Raw returns the underlying driver database, for anything this package
// doesn't wrap: aggregations, index management, transactions, RunCommand.
func (db *DB) Raw() *mongo.Database {
	return db.database
}
