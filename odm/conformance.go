package odm

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestConformance checks that a model type works the way this package
// expects — that its tags round-trip, that its collection resolves, that
// what it embeds does what embedding it implies:
//
//	func TestUserModel(t *testing.T) {
//		odm.TestConformance(t, odm.Use[User](database), func() *User {
//			return &User{Name: "Nana", Email: "nana@example.com"}
//		})
//	}
//
// Unlike cache.TestConformance and storage.TestConformance, which hold
// several backends to one interface, this holds one thing to another: your
// model to this package. There is only ever one implementation of an ODM
// here, so the useful question isn't whether it behaves — the suite next
// door answers that — but whether a model someone wrote wires into it
// correctly. The mistakes it catches are the quiet ones: an embedded
// odm.Model without `bson:",inline"`, two fields tagged to the same key, a
// relation field that isn't `bson:"-"` and so gets persisted.
//
// It adapts to what the model embeds: a model with odm.SoftDeletes is
// checked for soft-delete behavior, one with odm.Model for timestamps, one
// implementing odm.Indexer for index sync. A plain struct is held only to
// what a plain struct promises.
//
// Give it a collection of its own: it writes, reads and deletes documents,
// and expects to be the only thing doing so. newModel returns a fresh,
// unsaved model each time it is called.
func TestConformance[T any](t *testing.T, collection *Collection[T], newModel func() *T) {
	t.Helper()

	ctx := context.Background()
	meta := metaFor[T]()

	t.Run("resolves a collection name", func(t *testing.T) {
		if got := collection.Raw().Name(); got != meta.collection {
			t.Errorf("collection name = %q, want the resolved %q", got, meta.collection)
		}
	})

	t.Run("round-trips every persisted field", func(t *testing.T) {
		model := newModel()
		if err := collection.Create(ctx, model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		t.Cleanup(func() { purge(ctx, collection, model) })

		marshalled, err := bson.Marshal(model)
		if err != nil {
			t.Fatalf("marshalling the model: %v", err)
		}
		written := bson.Raw(marshalled)
		elements, err := written.Elements()
		if err != nil {
			t.Fatalf("reading the marshalled model: %v", err)
		}

		stored := storedDocument(t, ctx, collection, model)
		for _, element := range elements {
			value, err := stored.LookupErr(element.Key())
			if err != nil {
				t.Errorf("field %q never reached the document — check its bson tag, and that an embedded odm.Model carries `bson:\",inline\"`", element.Key())
				continue
			}
			if !sameRawValue(value, element.Value()) {
				t.Errorf("field %q stored as %v, want %v — two fields may be tagged to the same key", element.Key(), value, element.Value())
			}
		}
	})

	t.Run("finds what it created", func(t *testing.T) {
		model := newModel()
		if err := collection.Create(ctx, model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		t.Cleanup(func() { purge(ctx, collection, model) })

		count, err := collection.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count == 0 {
			t.Error("Count() = 0 after a Create — is this collection shared with something else?")
		}

		got, err := collection.Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) == 0 {
			t.Fatal("Get() returned nothing after a Create")
		}

		if id, ok := modelID(model); ok {
			if _, err := collection.Find(ctx, id); err != nil {
				t.Errorf("Find() error = %v, want the document just created", err)
			}
		}
	})

	if meta.trackable {
		t.Run("tracks the documents it reads and writes", func(t *testing.T) {
			model := newModel()
			if err := collection.Create(ctx, model); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			t.Cleanup(func() { purge(ctx, collection, model) })

			if !IsPersisted(model) {
				t.Error("IsPersisted() = false after Create — the model embeds odm.Model or odm.IdentityModel, so it should know")
			}
			if _, ok := modelID(model); !ok {
				t.Error("no _id on the model after Create — check that the embedded base carries `bson:\",inline\"`")
			}

			dirty, err := IsDirty(model)
			if err != nil {
				t.Fatalf("IsDirty() error = %v", err)
			}
			if dirty {
				t.Error("IsDirty() = true straight after Create, want a clean model")
			}

			// Saving a model with nothing changed must not fail, and must
			// leave it clean.
			if err := collection.Save(ctx, model); err != nil {
				t.Fatalf("Save() error = %v on an unchanged model", err)
			}
			if WasChanged(model) {
				t.Error("WasChanged() = true after a save that had nothing to write")
			}

			read, err := collection.First(ctx)
			if err != nil {
				t.Fatalf("First() error = %v", err)
			}
			if !IsPersisted(&read) {
				t.Error("IsPersisted() = false for a model that came from a read")
			}
		})
	}

	if meta.softDeletes {
		t.Run("soft-deletes, hides, restores and purges", func(t *testing.T) {
			model := newModel()
			if err := collection.Create(ctx, model); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			t.Cleanup(func() { purge(ctx, collection, model) })

			id, ok := modelID(model)
			if !ok {
				t.Skip("no _id on the model, so there is nothing to scope these to")
			}

			if _, err := collection.Where("_id", id).Delete(ctx); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := collection.Find(ctx, id); !errors.Is(err, ErrModelNotFound) {
				t.Errorf("Find() error = %v, want ErrModelNotFound for a soft-deleted document", err)
			}
			if _, err := collection.WithTrashed().Find(ctx, id); err != nil {
				t.Errorf("WithTrashed().Find() error = %v, want the soft-deleted document", err)
			}

			trashed, err := collection.OnlyTrashed().Count(ctx)
			if err != nil {
				t.Fatalf("OnlyTrashed().Count() error = %v", err)
			}
			if trashed == 0 {
				t.Error("OnlyTrashed().Count() = 0 after a Delete")
			}

			if _, err := collection.Where("_id", id).Restore(ctx); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if _, err := collection.Find(ctx, id); err != nil {
				t.Errorf("Find() error = %v, want the document visible again after Restore", err)
			}

			if _, err := collection.Where("_id", id).ForceDelete(ctx); err != nil {
				t.Fatalf("ForceDelete() error = %v", err)
			}
			if _, err := collection.WithTrashed().Find(ctx, id); !errors.Is(err, ErrModelNotFound) {
				t.Errorf("WithTrashed().Find() error = %v, want the document gone for good", err)
			}
		})
	} else {
		t.Run("deletes for real", func(t *testing.T) {
			model := newModel()
			if err := collection.Create(ctx, model); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			id, ok := modelID(model)
			if !ok {
				t.Skip("no _id on the model, so there is nothing to scope this to")
			}

			if _, err := collection.Where("_id", id).Delete(ctx); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := collection.Find(ctx, id); !errors.Is(err, ErrModelNotFound) {
				t.Errorf("Find() error = %v, want ErrModelNotFound after a Delete", err)
			}
		})
	}

	var zero T
	if _, ok := any(&zero).(Indexer); ok {
		t.Run("syncs its declared indexes, twice", func(t *testing.T) {
			if err := collection.SyncIndexes(ctx); err != nil {
				t.Fatalf("SyncIndexes() error = %v", err)
			}
			// Every start calls this, so it has to be safe to repeat.
			if err := collection.SyncIndexes(ctx); err != nil {
				t.Errorf("SyncIndexes() error = %v on a second run, want it to be idempotent", err)
			}
		})
	}
}

// modelID reads a model's _id out of its own BSON, which works whatever type
// the field is and without reflecting over the struct.
func modelID[T any](model *T) (any, bool) {
	marshalled, err := bson.Marshal(model)
	if err != nil {
		return nil, false
	}
	value, err := bson.Raw(marshalled).LookupErr("_id")
	if err != nil {
		return nil, false
	}

	var id any
	if err := value.Unmarshal(&id); err != nil {
		return nil, false
	}
	return id, true
}

// storedDocument reads back the document a model was written to, as MongoDB
// holds it.
func storedDocument[T any](t *testing.T, ctx context.Context, collection *Collection[T], model *T) bson.Raw {
	t.Helper()

	filter := bson.M{}
	if id, ok := modelID(model); ok {
		filter["_id"] = id
	}

	stored, err := collection.Raw().FindOne(ctx, filter).Raw()
	if err != nil {
		t.Fatalf("reading back the stored document: %v", err)
	}
	return stored
}

// purge removes a model however the model allows, so one subtest's document
// can't be mistaken for another's.
func purge[T any](ctx context.Context, collection *Collection[T], model *T) {
	id, ok := modelID(model)
	if !ok {
		return
	}

	query := collection.Where("_id", id)
	if collection.meta.softDeletes {
		_, _ = query.WithTrashed().ForceDelete(ctx)
		return
	}
	_, _ = query.Delete(ctx)
}
