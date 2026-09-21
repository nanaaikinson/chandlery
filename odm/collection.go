package odm

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Collection is a typed handle to one MongoDB collection. It is immutable
// once built, holds no query state of its own, and is safe to share across
// goroutines — every chain started from it gets a fresh Query.
type Collection[T any] struct {
	db         *DB
	collection *mongo.Collection
	meta       modelMeta
	// now is DB's clock, copied here so query compilation can stamp a
	// timestamp without reaching back through the database handle. Always
	// set by Use.
	now func() time.Time
}

// Use returns the collection for model T. The collection name comes from
// T's own CollectionName method when it has one, and otherwise from the
// lowercased type name plus "s" (see CollectionNamer).
//
// T must be the model type itself, not a pointer to it: Use[User], never
// Use[*User]. Both that and an unnameable T panic, since either is a wiring
// mistake that can't produce a working collection.
func Use[T any](db *DB) *Collection[T] {
	if db == nil {
		panic("odm: Use: db is nil")
	}
	meta := metaFor[T]()
	return &Collection[T]{
		db:         db,
		collection: db.database.Collection(meta.collection),
		meta:       meta,
		now:        db.now,
	}
}

// Raw returns the underlying driver collection, for aggregations, bulk
// writes, index management, or anything else this package doesn't wrap.
func (c *Collection[T]) Raw() *mongo.Collection {
	return c.collection
}

// Query starts an empty query. The chain-starting methods below (the Where
// family, Select, Exclude, OrderBy, Latest, Oldest, Limit, Skip) are
// shorthand for it, and the terminal ones (Get, First, Find, Count, Exists)
// run against the whole collection unfiltered.
//
// The mutating terminals — Update, UpdateOne, UpdateRaw, Delete, DeleteOne,
// Restore and ForceDelete — are deliberately not mirrored here. They act on
// every document the filter matches, so emptying or rewriting a whole
// collection takes an explicit users.Query().Delete(ctx) rather than a
// users.Delete(ctx) that reads like it might delete one thing. Create,
// CreateMany and Save are mirrored, because each acts on models you handed
// it rather than on whatever a filter reaches.
func (c *Collection[T]) Query() *Query[T] {
	return &Query[T]{collection: c}
}

// Scope starts a query with reusable transformations applied. See
// Query.Scope.
func (c *Collection[T]) Scope(scopes ...Scope[T]) *Query[T] {
	return c.Query().Scope(scopes...)
}

// WithTrashed starts a query including soft-deleted documents. See
// Query.WithTrashed.
func (c *Collection[T]) WithTrashed() *Query[T] {
	return c.Query().WithTrashed()
}

// OnlyTrashed starts a query over soft-deleted documents alone. See
// Query.OnlyTrashed.
func (c *Collection[T]) OnlyTrashed() *Query[T] {
	return c.Query().OnlyTrashed()
}

// With starts a query that eager-loads relations. See Query.With.
func (c *Collection[T]) With(relations ...Relation[T]) *Query[T] {
	return c.Query().With(relations...)
}

// Where starts a query with an equality or comparison condition. See
// Query.Where.
func (c *Collection[T]) Where(field string, args ...any) *Query[T] {
	return c.Query().Where(field, args...)
}

// WhereIn starts a query matching any of values. See Query.WhereIn.
func (c *Collection[T]) WhereIn(field string, values any) *Query[T] {
	return c.Query().WhereIn(field, values)
}

// WhereNotIn starts a query excluding any of values. See Query.WhereNotIn.
func (c *Collection[T]) WhereNotIn(field string, values any) *Query[T] {
	return c.Query().WhereNotIn(field, values)
}

// WhereNull starts a query matching a null or absent field. See
// Query.WhereNull.
func (c *Collection[T]) WhereNull(field string) *Query[T] {
	return c.Query().WhereNull(field)
}

// WhereNotNull starts a query matching a present, non-null field. See
// Query.WhereNotNull.
func (c *Collection[T]) WhereNotNull(field string) *Query[T] {
	return c.Query().WhereNotNull(field)
}

// WhereBetween starts a query on an inclusive range. See Query.WhereBetween.
func (c *Collection[T]) WhereBetween(field string, from, to any) *Query[T] {
	return c.Query().WhereBetween(field, from, to)
}

// WhereNotBetween starts a query outside a range. See
// Query.WhereNotBetween.
func (c *Collection[T]) WhereNotBetween(field string, from, to any) *Query[T] {
	return c.Query().WhereNotBetween(field, from, to)
}

// Select starts a query projecting only the named fields. See Query.Select.
func (c *Collection[T]) Select(fields ...string) *Query[T] {
	return c.Query().Select(fields...)
}

// Exclude starts a query dropping the named fields. See Query.Exclude.
func (c *Collection[T]) Exclude(fields ...string) *Query[T] {
	return c.Query().Exclude(fields...)
}

// WhereRaw starts a query from a driver-native filter. See Query.WhereRaw.
func (c *Collection[T]) WhereRaw(filter any) *Query[T] {
	return c.Query().WhereRaw(filter)
}

// OrderBy starts a sorted query. See Query.OrderBy.
func (c *Collection[T]) OrderBy(field string, direction Direction) *Query[T] {
	return c.Query().OrderBy(field, direction)
}

// Latest starts a newest-first query. See Query.Latest.
func (c *Collection[T]) Latest(fields ...string) *Query[T] {
	return c.Query().Latest(fields...)
}

// Oldest starts an oldest-first query. See Query.Oldest.
func (c *Collection[T]) Oldest(fields ...string) *Query[T] {
	return c.Query().Oldest(fields...)
}

// Limit starts a capped query. See Query.Limit.
func (c *Collection[T]) Limit(n int64) *Query[T] {
	return c.Query().Limit(n)
}

// Skip starts a query that discards the first n matches. See Query.Skip.
func (c *Collection[T]) Skip(n int64) *Query[T] {
	return c.Query().Skip(n)
}

// Get returns every document in the collection. See Query.Get.
func (c *Collection[T]) Get(ctx context.Context) ([]T, error) {
	return c.Query().Get(ctx)
}

// First returns the first document in the collection. See Query.First.
func (c *Collection[T]) First(ctx context.Context) (T, error) {
	return c.Query().First(ctx)
}

// Find returns the document with the given _id. See Query.Find.
func (c *Collection[T]) Find(ctx context.Context, id any) (T, error) {
	return c.Query().Find(ctx, id)
}

// Count reports how many documents the collection holds. See Query.Count.
func (c *Collection[T]) Count(ctx context.Context) (int64, error) {
	return c.Query().Count(ctx)
}

// CursorPaginate pages through the whole collection. See
// Query.CursorPaginate.
func (c *Collection[T]) CursorPaginate(ctx context.Context, page CursorPagination) (CursorPage[T], error) {
	return c.Query().CursorPaginate(ctx, page)
}

// Exists reports whether the collection holds anything. See Query.Exists.
func (c *Collection[T]) Exists(ctx context.Context) (bool, error) {
	return c.Query().Exists(ctx)
}

// Create inserts model. It takes a pointer so the fields it fills in are
// visible to the caller afterwards: a model embedding odm.Model or
// odm.IdentityModel gets a ULID _id (unless one is already set), and
// odm.Model also gets its CreatedAt/UpdatedAt stamped. A model that embeds
// neither is inserted exactly as given.
//
// The order is BeforeCreate, the Creating observers, then those
// assignments, then the insert, then AfterCreate and the Created observers —
// so a hook that sets its own ID or CreatedAt wins, and anything failing
// before the insert stops it. An error from the two that run afterwards is
// returned as-is, but the document is already written by then.
//
// A successful insert also leaves the model knowing it exists, so a later
// Save updates it rather than inserting it twice.
//
// It reports only an error. Mongo's own generated _id for a model with no
// _id field of its own is therefore not handed back — embed odm.Model, set
// your own _id, or use Raw().InsertOne when you need the InsertOneResult.
func (c *Collection[T]) Create(ctx context.Context, model *T) error {
	if model == nil {
		return ErrNilModel
	}

	return c.insert(ctx, model)
}

// insert is Create's body, shared with Save's insert path.
func (c *Collection[T]) insert(ctx context.Context, model *T) error {
	if err := runBeforeCreate(ctx, model); err != nil {
		return err
	}
	if err := notify(ctx, c.db, eventCreating, model); err != nil {
		return err
	}

	if hook, ok := any(model).(inserter); ok {
		hook.prepareForInsert(c.now())
	}

	if _, err := c.collection.InsertOne(ctx, model); err != nil {
		return classify(err)
	}

	// The model now agrees with the database, so a later Save sees an
	// update with nothing changed rather than a second insert.
	if err := snapshot(model); err != nil {
		return err
	}

	if err := runAfterCreate(ctx, model); err != nil {
		return err
	}
	return notify(ctx, c.db, eventCreated, model)
}
