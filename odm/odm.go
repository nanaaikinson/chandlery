// Package odm is a small MongoDB object-document mapper: a generic,
// copy-on-write query builder (odm.Query[T]) over a typed collection
// (odm.Collection[T]), shaped after Laravel Eloquent's developer experience
// but staying native MongoDB underneath. Filters are plain bson, sorts are
// plain bson, and every layer hands back the driver's own type — DB.Raw,
// Collection.Raw, Query.WhereRaw — so nothing here can trap a caller who
// needs something the builder doesn't cover.
//
// It is deliberately separate from this repo's db package: the two share no
// types and never import each other.
//
//	database := odm.New(client.Database("app"))
//	users := odm.Use[User](database)
//
//	user, err := users.
//		Where("email", "nana@example.com").
//		Where("is_active", true).
//		First(ctx)
package odm

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DB is a handle to one Mongo database. It holds no connection of its own —
// the *mongo.Client behind it, and closing that client, stays the caller's
// business.
type DB struct {
	database *mongo.Database
	now      func() time.Time

	// capabilities is probed lazily, on the first operation whose shape
	// depends on the server's version. See supportsSortedUpdateOne.
	capabilities serverCapabilities
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
