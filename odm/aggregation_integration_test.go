//go:build integration

package odm_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/nanaaikinson/chandlery/odm"
)

// countByStatus is a grouped result, shaped like nothing in the collection —
// what AggregateInto exists for.
type countByStatus struct {
	Status string `bson:"_id"`
	Total  int64  `bson:"total"`
}

func groupByStatus() mongo.Pipeline {
	return mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   "$status",
			"total": bson.M{"$sum": 1},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	}
}

func TestAggregate(t *testing.T) {
	t.Parallel()

	t.Run("decodes a grouped pipeline into another type", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "active"},
			&user{Name: "b", Status: "active"},
			&user{Name: "c", Status: "archived"},
		)

		got, err := odm.AggregateInto[user, countByStatus](ctx, users.Query(), groupByStatus())
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		want := []countByStatus{
			{Status: "active", Total: 2},
			{Status: "archived", Total: 1},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AggregateInto() = %v, want %v", got, want)
		}
	})

	t.Run("prepends the query's filter as a $match", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "active", Age: 30},
			&user{Name: "b", Status: "active", Age: 10},
			&user{Name: "c", Status: "archived", Age: 40},
		)

		got, err := odm.AggregateInto[user, countByStatus](ctx, users.Where("age", ">=", 18), groupByStatus())
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		want := []countByStatus{
			{Status: "active", Total: 1},
			{Status: "archived", Total: 1},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AggregateInto() = %v, want %v", got, want)
		}
	})

	t.Run("decodes into the model when the pipeline keeps its shape", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Age: 30},
			&user{Name: "b", Age: 10},
		)

		got, err := users.Aggregate(ctx, mongo.Pipeline{
			bson.D{{Key: "$match", Value: bson.M{"age": bson.M{"$gte": 18}}}},
		})
		if err != nil {
			t.Fatalf("Aggregate() error = %v", err)
		}
		if want := []string{"a"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Aggregate() = %v, want %v", names(got), want)
		}
	})

	t.Run("excludes soft-deleted documents", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes,
			&note{Title: "a", Author: "nana"},
			&note{Title: "gone", Author: "nana"},
		)
		if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		type byAuthor struct {
			Author string `bson:"_id"`
			Total  int64  `bson:"total"`
		}
		pipeline := mongo.Pipeline{
			bson.D{{Key: "$group", Value: bson.M{"_id": "$author", "total": bson.M{"$sum": 1}}}},
		}

		got, err := odm.AggregateInto[note, byAuthor](ctx, notes.Query(), pipeline)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		if want := []byAuthor{{Author: "nana", Total: 1}}; !reflect.DeepEqual(got, want) {
			t.Errorf("AggregateInto() = %v, want %v — the soft-delete scope applies", got, want)
		}

		// WithTrashed reaches them, like any other read.
		all, err := odm.AggregateInto[note, byAuthor](ctx, notes.WithTrashed(), pipeline)
		if err != nil {
			t.Fatalf("AggregateInto() error = %v", err)
		}
		if want := []byAuthor{{Author: "nana", Total: 2}}; !reflect.DeepEqual(all, want) {
			t.Errorf("WithTrashed AggregateInto() = %v, want %v", all, want)
		}
	})

	t.Run("Raw().Aggregate prepends nothing", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		notes := newNotes(t)
		seedNotes(t, notes, &note{Title: "a"}, &note{Title: "gone"})
		if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		cursor, err := notes.Raw().Aggregate(ctx, mongo.Pipeline{
			bson.D{{Key: "$count", Value: "total"}},
		})
		if err != nil {
			t.Fatalf("Aggregate() error = %v", err)
		}
		var counted []struct {
			Total int64 `bson:"total"`
		}
		if err := cursor.All(ctx, &counted); err != nil {
			t.Fatalf("All() error = %v", err)
		}
		if len(counted) != 1 || counted[0].Total != 2 {
			t.Errorf("raw aggregate counted %v, want both documents including the trashed one", counted)
		}
	})
}

func TestBulkWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	updated := &user{Name: "update-me", Status: "pending"}
	deleted := &user{Name: "delete-me", Status: "pending"}
	seed(t, users, updated, deleted)

	result, err := users.BulkWrite(ctx, []mongo.WriteModel{
		mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": updated.ID}).
			SetUpdate(bson.M{"$set": bson.M{"status": "active"}}),
		mongo.NewDeleteOneModel().
			SetFilter(bson.M{"_id": deleted.ID}),
		mongo.NewInsertOneModel().
			SetDocument(bson.M{"_id": "inserted", "name": "fresh", "status": "active"}),
	})
	if err != nil {
		t.Fatalf("BulkWrite() error = %v", err)
	}
	if result.ModifiedCount != 1 || result.DeletedCount != 1 || result.InsertedCount != 1 {
		t.Errorf("BulkWrite() modified/deleted/inserted = %d/%d/%d, want 1/1/1",
			result.ModifiedCount, result.DeletedCount, result.InsertedCount)
	}

	got, err := users.OrderBy("name", odm.Asc).Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if want := []string{"fresh", "update-me"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("Get() = %v, want %v", names(got), want)
	}
}

func TestCreateMany(t *testing.T) {
	t.Parallel()

	t.Run("inserts every model with its own id and timestamps", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		users := newUsers(t, clockAt(at))

		models := []*user{
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
		}
		if err := users.CreateMany(ctx, models); err != nil {
			t.Fatalf("CreateMany() error = %v", err)
		}

		seen := map[string]bool{}
		for _, model := range models {
			if model.ID == "" {
				t.Errorf("%q kept an empty ID", model.Name)
			}
			if seen[model.ID] {
				t.Errorf("%q reused an ID", model.Name)
			}
			seen[model.ID] = true
			if !model.CreatedAt.Equal(at) {
				t.Errorf("%q CreatedAt = %v, want %v", model.Name, model.CreatedAt, at)
			}
		}

		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 3 {
			t.Errorf("Count() = %d, want 3", count)
		}
	})

	t.Run("runs the create hooks on each model", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		docs := odm.Use[hooked](odm.New(testDatabase(t, client)))

		models := []*hooked{
			{Email: "  A@Example.com "},
			{Email: "B@EXAMPLE.COM"},
		}
		if err := docs.CreateMany(ctx, models); err != nil {
			t.Fatalf("CreateMany() error = %v", err)
		}

		for _, model := range models {
			if want := []string{"before", "after"}; !reflect.DeepEqual(model.calls, want) {
				t.Errorf("hook calls = %v, want %v", model.calls, want)
			}
		}

		stored, err := docs.OrderBy("email", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		got := []string{stored[0].Email, stored[1].Email}
		if want := []string{"a@example.com", "b@example.com"}; !reflect.DeepEqual(got, want) {
			t.Errorf("stored emails = %v, want %v", got, want)
		}
	})

	t.Run("an empty batch writes nothing", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)

		if err := users.CreateMany(ctx, nil); err != nil {
			t.Fatalf("CreateMany() error = %v", err)
		}
		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 0 {
			t.Errorf("Count() = %d, want 0", count)
		}
	})
}
