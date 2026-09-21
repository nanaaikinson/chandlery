//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

// note is the soft-deleting model: odm.Model for timestamps, odm.SoftDeletes
// for the deleted_at stamp.
type note struct {
	odm.Model       `bson:",inline"`
	odm.SoftDeletes `bson:",inline"`

	Title  string `bson:"title"`
	Author string `bson:"author"`
}

func (note) CollectionName() string { return "notes" }

func newNotes(t *testing.T, opts ...odm.Option) *odm.Collection[note] {
	t.Helper()

	return odm.Use[note](odm.New(testDatabase(t, client), opts...))
}

func seedNotes(t *testing.T, notes *odm.Collection[note], models ...*note) {
	t.Helper()

	for _, model := range models {
		if err := notes.Create(context.Background(), model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
}

func titles(models []note) []string {
	out := make([]string, len(models))
	for i, model := range models {
		out[i] = model.Title
	}
	return out
}

func TestSoftDelete(t *testing.T) {
	t.Parallel()

	t.Run("stamps deleted_at instead of removing the document", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		gone := &note{Title: "gone"}
		seedNotes(t, notes, gone, &note{Title: "kept"})

		result, err := notes.Where("title", "gone").Delete(ctx)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if result.DeletedCount != 1 {
			t.Errorf("Delete() deleted = %d, want 1", result.DeletedCount)
		}

		// The document is still physically there.
		raw, err := notes.Raw().CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("CountDocuments() error = %v", err)
		}
		if raw != 2 {
			t.Errorf("raw document count = %d, want 2 — a soft delete removes nothing", raw)
		}

		var stored bson.M
		if err := notes.Raw().FindOne(ctx, bson.M{"_id": gone.ID}).Decode(&stored); err != nil {
			t.Fatalf("FindOne() error = %v", err)
		}
		if stored["deleted_at"] == nil {
			t.Error("deleted_at is unset on a soft-deleted document")
		}
	})

	t.Run("hides soft-deleted documents from every read", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		gone := &note{Title: "gone"}
		seedNotes(t, notes, gone, &note{Title: "kept"})

		if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		got, err := notes.Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"kept"}; !reflect.DeepEqual(titles(got), want) {
			t.Errorf("Get() = %v, want %v", titles(got), want)
		}

		count, err := notes.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1", count)
		}

		exists, err := notes.Where("title", "gone").Exists(ctx)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if exists {
			t.Error("Exists() = true for a soft-deleted document")
		}

		if _, err := notes.Find(ctx, gone.ID); !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("Find() error = %v, want ErrModelNotFound", err)
		}
	})

	t.Run("WithTrashed and OnlyTrashed reach them again", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		gone := &note{Title: "gone"}
		seedNotes(t, notes, gone, &note{Title: "kept"})

		if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		all, err := notes.WithTrashed().OrderBy("title", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"gone", "kept"}; !reflect.DeepEqual(titles(all), want) {
			t.Errorf("WithTrashed().Get() = %v, want %v", titles(all), want)
		}

		trashed, err := notes.OnlyTrashed().Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"gone"}; !reflect.DeepEqual(titles(trashed), want) {
			t.Errorf("OnlyTrashed().Get() = %v, want %v", titles(trashed), want)
		}

		found, err := notes.WithTrashed().Find(ctx, gone.ID)
		if err != nil {
			t.Fatalf("WithTrashed().Find() error = %v", err)
		}
		if found.DeletedAt == nil {
			t.Error("DeletedAt is nil on a document fetched WithTrashed")
		}
	})

	t.Run("refreshes updated_at as it stamps", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		deleted := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		database := testDatabase(t, client)
		atCreate := odm.Use[note](odm.New(database, clockAt(created)))
		atDelete := odm.Use[note](odm.New(database, clockAt(deleted)))

		model := &note{Title: "gone"}
		seedNotes(t, atCreate, model)

		if _, err := atDelete.Where("_id", model.ID).Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		got, err := atCreate.WithTrashed().Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.DeletedAt == nil || !got.DeletedAt.Equal(deleted) {
			t.Errorf("DeletedAt = %v, want %v", got.DeletedAt, deleted)
		}
		if !got.UpdatedAt.Equal(deleted) {
			t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, deleted)
		}
	})

	t.Run("DeleteOne soft-deletes exactly one", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes,
			&note{Title: "a", Author: "nana"},
			&note{Title: "b", Author: "nana"},
		)

		result, err := notes.Where("author", "nana").DeleteOne(ctx)
		if err != nil {
			t.Fatalf("DeleteOne() error = %v", err)
		}
		if result.DeletedCount != 1 {
			t.Errorf("DeleteOne() deleted = %d, want 1", result.DeletedCount)
		}

		live, err := notes.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if live != 1 {
			t.Errorf("live count = %d, want 1", live)
		}
	})

	t.Run("deleting an already-trashed document counts nothing", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes, &note{Title: "gone"})

		if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		again, err := notes.Where("title", "gone").Delete(ctx)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if again.DeletedCount != 0 {
			t.Errorf("second Delete() deleted = %d, want 0", again.DeletedCount)
		}
	})
}

func TestRestore(t *testing.T) {
	t.Parallel()

	t.Run("brings a soft-deleted document back", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		model := &note{Title: "gone"}
		seedNotes(t, notes, model)

		if _, err := notes.Where("_id", model.ID).Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		result, err := notes.Where("_id", model.ID).Restore(ctx)
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if result.ModifiedCount != 1 {
			t.Errorf("Restore() modified = %d, want 1", result.ModifiedCount)
		}

		got, err := notes.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v, want the document visible again", err)
		}
		if got.DeletedAt != nil {
			t.Errorf("DeletedAt = %v, want nil", got.DeletedAt)
		}

		// Restore unsets the field rather than nulling it, so a restored
		// document is shaped exactly like one that was never deleted.
		var raw bson.M
		if err := notes.Raw().FindOne(ctx, bson.M{"_id": model.ID}).Decode(&raw); err != nil {
			t.Fatalf("FindOne() error = %v", err)
		}
		if _, ok := raw["deleted_at"]; ok {
			t.Error("deleted_at is still present after a restore, want the field gone")
		}
	})

	t.Run("leaves live documents alone", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes, &note{Title: "live"})

		result, err := notes.Query().Restore(ctx)
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if result.ModifiedCount != 0 {
			t.Errorf("Restore() modified = %d, want 0", result.ModifiedCount)
		}
	})
}

func TestForceDelete(t *testing.T) {
	t.Parallel()

	t.Run("removes live and trashed documents alike", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes,
			&note{Title: "trashed", Author: "nana"},
			&note{Title: "live", Author: "nana"},
		)

		if _, err := notes.Where("title", "trashed").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		result, err := notes.Where("author", "nana").ForceDelete(ctx)
		if err != nil {
			t.Fatalf("ForceDelete() error = %v", err)
		}
		if result.DeletedCount != 2 {
			t.Errorf("ForceDelete() deleted = %d, want 2 — the default scope must not hide the trashed one", result.DeletedCount)
		}

		raw, err := notes.Raw().CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("CountDocuments() error = %v", err)
		}
		if raw != 0 {
			t.Errorf("raw document count = %d, want 0", raw)
		}
	})

	t.Run("OnlyTrashed narrows it to a purge", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes,
			&note{Title: "trashed"},
			&note{Title: "live"},
		)

		if _, err := notes.Where("title", "trashed").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		result, err := notes.OnlyTrashed().ForceDelete(ctx)
		if err != nil {
			t.Fatalf("ForceDelete() error = %v", err)
		}
		if result.DeletedCount != 1 {
			t.Errorf("ForceDelete() deleted = %d, want 1", result.DeletedCount)
		}

		remaining, err := notes.Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"live"}; !reflect.DeepEqual(titles(remaining), want) {
			t.Errorf("Get() = %v, want %v", titles(remaining), want)
		}
	})
}
