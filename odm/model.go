package odm

import (
	"time"

	"github.com/oklog/ulid/v2"
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

// IdentityModel is an optional base struct carrying just a ULID primary key,
// assigned by Create when it isn't already set. Embed it inline so the field
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
// ID is a string rather than a ulid.ULID so it stores as a readable,
// lexicographically sortable Mongo string — matching db.IdentityModel, and
// avoiding a raw 16-byte array in the document. Use your own _id field if
// you want a different type; Find takes an id of any type.
type IdentityModel struct {
	ID string `bson:"_id" json:"id"`

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

// prepareForInsert assigns a ULID if ID is unset, so a caller-supplied ID
// survives.
func (m *IdentityModel) prepareForInsert(_ time.Time) {
	if m.ID == "" {
		m.ID = ulid.Make().String()
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
