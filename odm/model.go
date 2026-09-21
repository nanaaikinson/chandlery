package odm

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Model is an optional base struct carrying a ULID primary key and
// created_at/updated_at timestamps, filled in by Collection.Create. Embed it
// inline so its fields land at the top level of the document rather than
// nested under a "Model" key:
//
//	type User struct {
//		odm.Model `bson:",inline"`
//
//		Name string `bson:"name"`
//	}
//
// Nothing requires a model to embed it — a struct with its own `bson:"_id"`
// field (or none at all, letting Mongo generate an ObjectID) works the same
// everywhere else in this package.
//
// ID is a string rather than a ulid.ULID so it stores as a readable,
// lexicographically sortable Mongo string — matching db.IdentityModel, and
// avoiding a raw 16-byte array in the document. Use your own _id field if
// you want a different type; Find takes an id of any type.
type Model struct {
	ID        string    `bson:"_id" json:"id"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// inserter is the whole of Phase 1's insert lifecycle: one unexported hook,
// implemented only by *Model, so embedding odm.Model is the single way to
// opt in and no reflection over a model's fields is needed to find it. A
// general hook/observer system is a later milestone.
type inserter interface {
	prepareForInsert(now time.Time)
}

// prepareForInsert assigns a ULID if ID is unset (so a caller-supplied ID
// survives), stamps CreatedAt without overwriting a caller-supplied value
// (e.g. for backfills), and always refreshes UpdatedAt — the same rules as
// db.Model's BeforeAppendModel.
func (m *Model) prepareForInsert(now time.Time) {
	if m.ID == "" {
		m.ID = ulid.Make().String()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
}

// CollectionNamer lets a model name its own collection:
//
//	func (User) CollectionName() string { return "users" }
//
// Either a value or a pointer receiver works.
type CollectionNamer interface {
	CollectionName() string
}

// collectionNames caches the resolved name per model type. Resolution reads
// a type's name and checks one interface, both of which are fixed for the
// life of the process — so it runs once per T, not once per query.
var collectionNames sync.Map // reflect.Type -> string

func collectionName[T any]() string {
	t := reflect.TypeFor[T]()
	if name, ok := collectionNames.Load(t); ok {
		return name.(string)
	}

	name := resolveCollectionName[T](t)
	collectionNames.Store(t, name)
	return name
}

// resolveCollectionName prefers the model's own CollectionName. Failing
// that, it falls back to the lowercased type name with an "s" appended —
// User -> "users". That is all the pluralization there is: a model whose
// plural isn't its name plus "s" (Person -> "persons", Company ->
// "companys") should spell the collection out via CollectionNamer rather
// than expect this to guess.
func resolveCollectionName[T any](t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		panic("odm: Use[" + t.String() + "]: T must be the model type itself, not a pointer")
	}

	var zero T
	if namer, ok := any(zero).(CollectionNamer); ok {
		return namer.CollectionName()
	}
	if namer, ok := any(&zero).(CollectionNamer); ok {
		return namer.CollectionName()
	}

	if t.Kind() != reflect.Struct {
		panic("odm: Use[" + t.String() + "]: T must be a struct, or implement odm.CollectionNamer")
	}
	if t.Name() == "" {
		panic("odm: Use: an anonymous struct has no name to derive a collection from — implement odm.CollectionNamer")
	}
	return strings.ToLower(t.Name()) + "s"
}
