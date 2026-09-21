//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

// article is the model-state suite's model: an omitempty field so a value
// can go away, and hooks that record what they saw.
type article struct {
	odm.Model `bson:",inline"`

	Title    string `bson:"title"`
	Subtitle string `bson:"subtitle,omitempty"`
	Views    int    `bson:"views"`

	normalized bool
	updates    []string
}

func (article) CollectionName() string { return "articles" }

func (a *article) BeforeUpdate(_ context.Context) error {
	a.updates = append(a.updates, "before")
	a.Title = strings.TrimSpace(a.Title)
	a.normalized = true
	return nil
}

func (a *article) AfterUpdate(_ context.Context) error {
	a.updates = append(a.updates, "after")
	return nil
}

func newArticles(t *testing.T, opts ...odm.Option) *odm.Collection[article] {
	t.Helper()

	return odm.Use[article](odm.New(testDatabase(t, client), opts...))
}

// storedArticle reads a document as MongoDB actually holds it.
func storedArticle(t *testing.T, articles *odm.Collection[article], id string) bson.M {
	t.Helper()

	var stored bson.M
	if err := articles.Raw().FindOne(context.Background(), bson.M{"_id": id}).Decode(&stored); err != nil {
		t.Fatalf("FindOne() error = %v", err)
	}
	return stored
}

func TestSave(t *testing.T) {
	t.Parallel()

	t.Run("inserts a model that does not exist yet", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if model.ID == "" {
			t.Error("Save() left ID empty, want an inserted model to be identified")
		}
		if !odm.IsPersisted(model) {
			t.Error("IsPersisted() = false after an insert")
		}

		count, err := articles.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1", count)
		}
	})

	t.Run("inserts even when the caller chose the ID", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "chosen"}
		model.ID = "my-own-id"
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		// Existence comes from state, not from a non-zero ID.
		if _, err := articles.Find(ctx, "my-own-id"); err != nil {
			t.Errorf("Find() error = %v, want the model inserted under its chosen id", err)
		}
	})

	t.Run("updates only what changed", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first", Views: 1}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		// Another writer touches a field this model isn't changing.
		if _, err := articles.Raw().UpdateOne(ctx, bson.M{"_id": model.ID}, bson.M{
			"$set": bson.M{"views": 99},
		}); err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}

		loaded, err := articles.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		stale := loaded
		stale.Views = 99 // what it was read as
		stale.Title = "renamed"
		if err := articles.Save(ctx, &stale); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		stored := storedArticle(t, articles, model.ID)
		if stored["title"] != "renamed" {
			t.Errorf("title = %v, want the change written", stored["title"])
		}
		if stored["views"] != int32(99) {
			t.Errorf("views = %v, want the other writer's value kept — Save is a $set of changes, not a replacement", stored["views"])
		}
	})

	t.Run("unsets a field that went away", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first", Subtitle: "a subtitle"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Subtitle = ""
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		stored := storedArticle(t, articles, model.ID)
		if _, ok := stored["subtitle"]; ok {
			t.Errorf("subtitle = %v, want the field removed", stored["subtitle"])
		}
	})

	t.Run("writes nothing when nothing changed", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		database := testDatabase(t, client)
		atCreate := odm.Use[article](odm.New(database, clockAt(created)))
		atSave := odm.Use[article](odm.New(database, clockAt(later)))

		model := &article{Title: "first"}
		if err := atCreate.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		if err := atSave.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		// A write would have moved updated_at.
		stored := storedArticle(t, atCreate, model.ID)
		updated := stored["updated_at"].(bson.DateTime).Time().UTC()
		if !updated.Equal(created) {
			t.Errorf("updated_at = %v, want it untouched at %v — an unchanged model should not be written", updated, created)
		}
	})

	t.Run("refreshes updated_at on a real change", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		later := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

		database := testDatabase(t, client)
		atCreate := odm.Use[article](odm.New(database, clockAt(created)))
		atSave := odm.Use[article](odm.New(database, clockAt(later)))

		model := &article{Title: "first"}
		if err := atCreate.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Title = "second"
		if err := atSave.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		if !model.UpdatedAt.Equal(later) {
			t.Errorf("model UpdatedAt = %v, want %v", model.UpdatedAt, later)
		}
		stored := storedArticle(t, atCreate, model.ID)
		if updated := stored["updated_at"].(bson.DateTime).Time().UTC(); !updated.Equal(later) {
			t.Errorf("stored updated_at = %v, want %v", updated, later)
		}
	})

	t.Run("leaves the model clean afterwards", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Title = "second"
		dirty, err := odm.IsDirty(model)
		if err != nil {
			t.Fatalf("IsDirty() error = %v", err)
		}
		if !dirty {
			t.Fatal("IsDirty() = false after a change")
		}

		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if dirty, err = odm.IsDirty(model); err != nil {
			t.Fatalf("IsDirty() error = %v", err)
		}
		if dirty {
			t.Error("IsDirty() = true after a successful save")
		}
	})

	t.Run("runs the update hooks and writes what they changed", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Title = "  spaced out  "
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		if want := []string{"before", "after"}; !reflect.DeepEqual(model.updates, want) {
			t.Errorf("hook calls = %v, want %v", model.updates, want)
		}
		// BeforeUpdate trimmed the title, and the update was recomputed
		// after it ran.
		stored := storedArticle(t, articles, model.ID)
		if stored["title"] != "spaced out" {
			t.Errorf("title = %q, want the hook's trimmed value", stored["title"])
		}
	})

	t.Run("does not run the update hooks for an unchanged model", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if len(model.updates) != 0 {
			t.Errorf("hook calls = %v, want none for a clean model", model.updates)
		}
	})

	t.Run("saves a model read back from the database", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		if err := articles.Save(ctx, &article{Title: "first"}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		loaded, err := articles.First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if !odm.IsPersisted(&loaded) {
			t.Fatal("IsPersisted() = false for a model that came from a read")
		}

		loaded.Title = "second"
		if err := articles.Save(ctx, &loaded); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		count, err := articles.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1 — saving a hydrated model must update, not insert", count)
		}
	})

	t.Run("models from Get are saveable too", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		for _, title := range []string{"a", "b"} {
			if err := articles.Save(ctx, &article{Title: title}); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
		}

		loaded, err := articles.OrderBy("title", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		loaded[0].Views = 10
		if err := articles.Save(ctx, &loaded[0]); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		stored := storedArticle(t, articles, loaded[0].ID)
		if stored["views"] != int32(10) {
			t.Errorf("views = %v, want 10", stored["views"])
		}
		if count, _ := articles.Count(ctx); count != 2 {
			t.Errorf("Count() = %d, want 2", count)
		}
	})

	t.Run("leaves a field the model does not know about alone", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		articles := newArticles(t)

		// A document carrying more than the struct declares — an older
		// schema, or another service's field.
		if _, err := articles.Raw().InsertOne(ctx, bson.M{
			"_id":      "legacy",
			"title":    "first",
			"views":    1,
			"legacy":   "do not lose me",
			"added_by": "another service",
		}); err != nil {
			t.Fatalf("InsertOne() error = %v", err)
		}

		loaded, err := articles.Find(ctx, "legacy")
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		loaded.Title = "second"
		if err := articles.Save(ctx, &loaded); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		stored := storedArticle(t, articles, "legacy")
		if stored["legacy"] != "do not lose me" || stored["added_by"] != "another service" {
			t.Errorf("stored = %v, want the unknown fields untouched", stored)
		}
		if stored["title"] != "second" {
			t.Errorf("title = %v, want the change written", stored["title"])
		}
	})

	t.Run("rejects a nil model", func(t *testing.T) {
		t.Parallel()

		if err := newArticles(t).Save(context.Background(), nil); !errors.Is(err, odm.ErrNilModel) {
			t.Errorf("Save() error = %v, want ErrNilModel", err)
		}
	})
}

// watcher records the observer events it sees, and what the model looked
// like at the time.
type watcher struct {
	mu     sync.Mutex
	events []string
	titles []string
}

func (w *watcher) Creating(_ context.Context, a *article) error {
	w.record("creating", a)
	return nil
}

func (w *watcher) Created(_ context.Context, a *article) error {
	w.record("created", a)
	return nil
}

func (w *watcher) Updating(_ context.Context, a *article) error {
	w.record("updating", a)
	return nil
}

func (w *watcher) Updated(_ context.Context, a *article) error {
	w.record("updated", a)
	return nil
}

func (w *watcher) record(event string, a *article) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	w.titles = append(w.titles, a.Title)
}

func TestObservers(t *testing.T) {
	t.Parallel()

	t.Run("sees a create and an update", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		seen := &watcher{}
		odm.Observe[article](database, seen)
		articles := odm.Use[article](database)

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Title = "second"
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		want := []string{"creating", "created", "updating", "updated"}
		if !reflect.DeepEqual(seen.events, want) {
			t.Errorf("events = %v, want %v", seen.events, want)
		}
		// Updating runs before the write, Updated after — both see the new
		// value, since the model is what carries it.
		if seen.titles[2] != "second" || seen.titles[3] != "second" {
			t.Errorf("titles at update = %v, want the changed one", seen.titles[2:])
		}
	})

	t.Run("an observer sees what is about to change", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		articles := odm.Use[article](database)

		var wasTitle string
		var changed bson.M
		odm.Observe[article](database, changeWatcher{
			onUpdating: func(a *article) error {
				if original, ok := odm.Original(a, "title"); ok {
					_ = original.Unmarshal(&wasTitle)
				}
				got, err := odm.Changes(a)
				changed = got.Set
				return err
			},
		})

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		model.Title = "second"
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		if wasTitle != "first" {
			t.Errorf("Original(title) = %q, want %q", wasTitle, "first")
		}
		if want := (bson.M{"title": "second"}); !reflect.DeepEqual(changed, want) {
			t.Errorf("Changes() = %v, want %v", changed, want)
		}
	})

	t.Run("a failing observer aborts the write", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		articles := odm.Use[article](database)

		sentinel := errors.New("not allowed")
		odm.Observe[article](database, changeWatcher{
			onUpdating: func(*article) error { return sentinel },
		})

		model := &article{Title: "first"}
		if err := articles.Save(ctx, model); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		model.Title = "second"
		if err := articles.Save(ctx, model); !errors.Is(err, sentinel) {
			t.Fatalf("Save() error = %v, want the observer's own error", err)
		}

		stored := storedArticle(t, articles, model.ID)
		if stored["title"] != "first" {
			t.Errorf("title = %v, want the write aborted", stored["title"])
		}
	})

	t.Run("sees a plain Create too", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		seen := &watcher{}
		odm.Observe[article](database, seen)
		articles := odm.Use[article](database)

		if err := articles.Create(ctx, &article{Title: "first"}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if want := []string{"creating", "created"}; !reflect.DeepEqual(seen.events, want) {
			t.Errorf("events = %v, want %v", seen.events, want)
		}
	})
}

// changeWatcher observes updates through a supplied function.
type changeWatcher struct {
	onUpdating func(*article) error
}

func (w changeWatcher) Updating(_ context.Context, a *article) error {
	return w.onUpdating(a)
}
