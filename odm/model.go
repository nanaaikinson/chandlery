package odm

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The field names this package writes to on a model's behalf. They match
// odm.Model and odm.SoftDeletes' own bson tags: embedding those structs is
// what opts a model into the behavior, so the names are fixed rather than
// configurable.
const (
	createdAtField = "created_at"
	updatedAtField = "updated_at"
	deletedAtField = "deleted_at"
)

// IdentityModel is an optional base struct carrying just an ObjectID primary
// key, assigned by Create when it isn't already set. Embed it inline so the field
// lands at the top level of the document:
//
//	type Session struct {
//		odm.IdentityModel `bson:",inline"`
//
//		Token string `bson:"token"`
//	}
//
// Use this instead of Model for a collection with no created_at/updated_at,
// mirroring db.IdentityModel. Nothing requires a model to embed either — a
// struct with its own `bson:"_id"` field, or none at all (letting MongoDB
// generate an ObjectID), works the same everywhere else in this package.
//
// ID is a native bson.ObjectID — the same type MongoDB itself generates for
// a document inserted without an _id — so it indexes, sorts by creation
// time and round-trips through other MongoDB tooling as a real ObjectID. To
// choose the ID yourself, set it before Create (or in a BeforeCreate hook):
// a non-zero ID is never overwritten.
//
// To use a different _id type, such as a ULID string, declare your own ID
// field next to the embed. It shadows this one — in Go, and in the BSON
// codec, which resolves duplicate inline keys by the same rule — and you
// keep the timestamps and dirty tracking:
//
//	type Tenant struct {
//		odm.Model `bson:",inline"`
//		ID        string `bson:"_id" json:"id"`
//	}
//
//	func (t *Tenant) BeforeCreate(ctx context.Context) error {
//		if t.ID == "" {
//			t.ID = ulid.Make().String()
//		}
//		return nil
//	}
//
// Create only generates an ObjectID for this struct's own field, so once
// you shadow it, assigning the ID is yours: leave it empty and the document
// is stored under _id "". Find takes an id of any type.
type IdentityModel struct {
	ID bson.ObjectID `bson:"_id" json:"id"`

	// state is this package's own bookkeeping — whether the model came from
	// the database, and what it looked like then. Unexported, so the BSON
	// codec never sees it and it is never written anywhere. See state.go.
	state modelState
}

// Model is IdentityModel plus created_at/updated_at, which Create stamps on
// insert and Update refreshes:
//
//	type User struct {
//		odm.Model `bson:",inline"`
//
//		Name string `bson:"name"`
//	}
//
// Embedding it is what opts a model into timestamps — see Query.Update and
// Query.WithoutTimestamps.
type Model struct {
	IdentityModel `bson:",inline"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// inserter is the insert-time half of this package's model lifecycle: one
// unexported hook, implemented only by *IdentityModel and *Model, so
// embedding one of them is the single way to opt in and no reflection over a
// model's fields is needed to find it. Caller-facing lifecycle hooks are
// separate and exported — see hooks.go.
type inserter interface {
	prepareForInsert(now time.Time)
}

// prepareForInsert assigns a new ObjectID if ID is unset, so a
// caller-supplied ID survives.
func (m *IdentityModel) prepareForInsert(_ time.Time) {
	if m.ID.IsZero() {
		m.ID = bson.NewObjectID()
	}
}

// prepareForInsert delegates to IdentityModel for ID assignment (Go's method
// shadowing means it wouldn't otherwise run), stamps CreatedAt without
// overwriting a caller-supplied value (e.g. for backfills), and always
// refreshes UpdatedAt — the same rules as db.Model's BeforeAppendModel.
func (m *Model) prepareForInsert(now time.Time) {
	m.IdentityModel.prepareForInsert(now)

	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
}

// timestamped marks a model that carries created_at/updated_at. The marker
// is what lets a query-level Update refresh updated_at with no instance to
// inspect and no reflection over fields; the method itself is what lets Save
// stamp the instance it does have. Only *Model implements it.
type timestamped interface {
	touchUpdatedAt(now time.Time)
}

func (m *Model) touchUpdatedAt(now time.Time) { m.UpdatedAt = now }

// CollectionNamer lets a model name its own collection:
//
//	func (User) CollectionName() string { return "users" }
//
// Either a value or a pointer receiver works.
type CollectionNamer interface {
	CollectionName() string
}
