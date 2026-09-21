//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

// hooked exercises the create hooks against a real insert.
type hooked struct {
	odm.Model `bson:",inline"`

	Email string `bson:"email"`

	// Unexported, so none of this reaches MongoDB.
	calls      []string
	idAtBefore string
	idAtAfter  string
	afterError error
}

func (hooked) CollectionName() string { return "hooked" }

func (h *hooked) BeforeCreate(_ context.Context) error {
	h.calls = append(h.calls, "before")
	h.idAtBefore = h.ID
	h.Email = strings.ToLower(strings.TrimSpace(h.Email))
	return nil
}

func (h *hooked) AfterCreate(_ context.Context) error {
	h.calls = append(h.calls, "after")
	h.idAtAfter = h.ID
	return h.afterError
}

func TestCreateHooks(t *testing.T) {
	t.Parallel()

	t.Run("runs either side of the insert and can rewrite the model", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		docs := odm.Use[hooked](odm.New(testDatabase(t, client)))

		model := hooked{Email: "  Nana@Example.COM "}
		if err := docs.Create(ctx, &model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if want := []string{"before", "after"}; !reflect.DeepEqual(model.calls, want) {
			t.Errorf("hook calls = %v, want %v", model.calls, want)
		}
		// BeforeCreate runs ahead of the ULID assignment, so a hook that
		// wants to choose the id still can; AfterCreate sees the real one.
		if model.idAtBefore != "" {
			t.Errorf("BeforeCreate saw ID = %q, want it unassigned", model.idAtBefore)
		}
		if model.idAtAfter == "" || model.idAtAfter != model.ID {
			t.Errorf("AfterCreate saw ID = %q, want the assigned %q", model.idAtAfter, model.ID)
		}

		stored, err := docs.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if stored.Email != "nana@example.com" {
			t.Errorf("stored email = %q, want the hook's normalized value", stored.Email)
		}
	})

	t.Run("an AfterCreate failure reaches the caller with the document already written", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		docs := odm.Use[hooked](odm.New(testDatabase(t, client)))

		sentinel := errors.New("notification failed")
		model := hooked{Email: "nana@example.com", afterError: sentinel}

		err := docs.Create(ctx, &model)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Create() error = %v, want it to wrap the hook's error", err)
		}

		// The insert already happened — AfterCreate cannot undo it, which
		// is exactly what the interface documents.
		if _, err := docs.Find(ctx, model.ID); err != nil {
			t.Errorf("Find() error = %v, want the inserted document to still be there", err)
		}
	})
}

func TestUpdateTimestamps(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("Update refreshes updated_at and leaves created_at", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := testDatabase(t, client)
		atCreate := odm.Use[user](odm.New(database, clockAt(created)))
		atUpdate := odm.Use[user](odm.New(database, clockAt(updated)))

		model := &user{Name: "Nana"}
		seed(t, atCreate, model)

		if _, err := atUpdate.Where("_id", model.ID).Set("name", "Nana Kwesi").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		got, err := atCreate.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if !got.CreatedAt.Equal(created) {
			t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
		}
		if !got.UpdatedAt.Equal(updated) {
			t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
		}
		if got.Name != "Nana Kwesi" {
			t.Errorf("Name = %q, want the updated value", got.Name)
		}
	})

	t.Run("WithoutTimestamps leaves updated_at alone", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := testDatabase(t, client)
		atCreate := odm.Use[user](odm.New(database, clockAt(created)))
		atUpdate := odm.Use[user](odm.New(database, clockAt(updated)))

		model := &user{Name: "Nana"}
		seed(t, atCreate, model)

		if _, err := atUpdate.
			Where("_id", model.ID).
			Inc("login_count", 1).
			WithoutTimestamps().
			Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		got, err := atCreate.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if !got.UpdatedAt.Equal(created) {
			t.Errorf("UpdatedAt = %v, want it untouched at %v", got.UpdatedAt, created)
		}
		if got.LoginCount != 1 {
			t.Errorf("LoginCount = %d, want 1", got.LoginCount)
		}
	})

	t.Run("UpdateRaw is left entirely to the caller", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := testDatabase(t, client)
		atCreate := odm.Use[user](odm.New(database, clockAt(created)))
		atUpdate := odm.Use[user](odm.New(database, clockAt(updated)))

		model := &user{Name: "Nana"}
		seed(t, atCreate, model)

		if _, err := atUpdate.Where("_id", model.ID).UpdateRaw(ctx, bson.M{
			"$set": bson.M{"name": "Nana Kwesi"},
		}); err != nil {
			t.Fatalf("UpdateRaw() error = %v", err)
		}

		got, err := atCreate.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if !got.UpdatedAt.Equal(created) {
			t.Errorf("UpdatedAt = %v, want a raw update to stamp nothing", got.UpdatedAt)
		}
	})
}

func TestScopes(t *testing.T) {
	t.Parallel()

	active := func(q *odm.Query[user]) *odm.Query[user] { return q.Where("is_active", true) }
	adults := func(q *odm.Query[user]) *odm.Query[user] { return q.Where("age", ">=", 18) }

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "active-adult", IsActive: true, Age: 30},
		&user{Name: "active-minor", IsActive: true, Age: 12},
		&user{Name: "inactive-adult", IsActive: false, Age: 40},
	)

	t.Run("one scope filters", func(t *testing.T) {
		t.Parallel()

		got, err := users.Scope(active).OrderBy("name", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"active-adult", "active-minor"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("scopes compose with each other and with plain conditions", func(t *testing.T) {
		t.Parallel()

		got, err := users.Scope(active, adults).Where("name", "!=", "nobody").Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"active-adult"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})
}
