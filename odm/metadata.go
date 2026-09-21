package odm

import (
	"reflect"
	"strings"
	"sync"
)

// modelMeta is everything this package needs to know about a model type.
// All of it is fixed for the life of the process, so it is resolved once per
// T and cached — reflection and interface checks never run per query.
type modelMeta struct {
	collection  string
	timestamps  bool
	softDeletes bool
	// trackable models can remember the document they came from, which is
	// what Save diffs against. Only models embedding odm.Model or
	// odm.IdentityModel can: the state has to live somewhere.
	trackable bool
}

// modelMetas caches metadata per model type. Keyed by reflect.Type, which is
// bounded by the program's own types rather than by anything at runtime.
var modelMetas sync.Map // reflect.Type -> modelMeta

func metaFor[T any]() modelMeta {
	t := reflect.TypeFor[T]()
	if meta, ok := modelMetas.Load(t); ok {
		return meta.(modelMeta)
	}

	meta := resolveMeta[T](t)
	modelMetas.Store(t, meta)
	return meta
}

func resolveMeta[T any](t reflect.Type) modelMeta {
	if t.Kind() == reflect.Pointer {
		panic("odm: Use[" + t.String() + "]: T must be the model type itself, not a pointer")
	}

	var zero T
	_, timestamps := any(&zero).(timestamped)
	_, softDeletes := any(&zero).(softDeletable)
	_, trackable := any(&zero).(stateful)

	return modelMeta{
		collection:  resolveCollectionName[T](t),
		timestamps:  timestamps,
		softDeletes: softDeletes,
		trackable:   trackable,
	}
}

// resolveCollectionName prefers the model's own CollectionName. Failing
// that, it falls back to the lowercased type name with an "s" appended —
// User -> "users". That is all the pluralization there is: a model whose
// plural isn't its name plus "s" (Person -> "persons", Company ->
// "companys") should spell the collection out via CollectionNamer rather
// than expect this to guess.
func resolveCollectionName[T any](t reflect.Type) string {
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
