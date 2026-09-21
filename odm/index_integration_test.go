//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/nanaaikinson/chandlery/odm"
)

// account declares indexes, including a unique one to violate.
type account struct {
	odm.Model `bson:",inline"`

	Email      string `bson:"email"`
	BusinessID string `bson:"business_id"`
}

func (account) CollectionName() string { return "accounts" }

func (account) Indexes() []odm.Index {
	return []odm.Index{
		{
			Keys:   bson.D{{Key: "email", Value: 1}},
			Unique: true,
		},
		{
			Keys: bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}},
			Name: "business_recent",
		},
	}
}

func newAccounts(t *testing.T) *odm.Collection[account] {
	t.Helper()

	return odm.Use[account](odm.New(testDatabase(t, client)))
}

// indexNames lists what the collection actually has, straight from MongoDB.
func indexNames(t *testing.T, accounts *odm.Collection[account]) []string {
	t.Helper()

	ctx := context.Background()
	cursor, err := accounts.Raw().Indexes().List(ctx)
	if err != nil {
		t.Fatalf("Indexes().List() error = %v", err)
	}

	var specs []struct {
		Name string `bson:"name"`
	}
	if err := cursor.All(ctx, &specs); err != nil {
		t.Fatalf("All() error = %v", err)
	}

	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	sort.Strings(names)
	return names
}

func TestSyncIndexes(t *testing.T) {
	t.Parallel()

	t.Run("creates every declared index, compound ones included", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)

		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("SyncIndexes() error = %v", err)
		}

		// _id_ is MongoDB's own; email_1 is the generated name; the
		// compound one took the name it declared.
		want := []string{"_id_", "business_recent", "email_1"}
		if got := indexNames(t, accounts); !reflect.DeepEqual(got, want) {
			t.Errorf("indexes = %v, want %v", got, want)
		}
	})

	t.Run("is safe to run again", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)

		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("first SyncIndexes() error = %v", err)
		}
		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("second SyncIndexes() error = %v — it must be safe on every start", err)
		}

		want := []string{"_id_", "business_recent", "email_1"}
		if got := indexNames(t, accounts); !reflect.DeepEqual(got, want) {
			t.Errorf("indexes = %v, want %v", got, want)
		}
	})

	t.Run("a model declaring none says so", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		if err := users.SyncIndexes(context.Background()); err == nil {
			t.Error("SyncIndexes() error = nil, want a complaint that the model declares none")
		}
	})
}

func TestDuplicateKey(t *testing.T) {
	t.Parallel()

	t.Run("Create reports a unique violation as ErrDuplicateKey", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)
		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("SyncIndexes() error = %v", err)
		}

		if err := accounts.Create(ctx, &account{Email: "nana@example.com"}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		err := accounts.Create(ctx, &account{Email: "nana@example.com"})
		if !errors.Is(err, odm.ErrDuplicateKey) {
			t.Fatalf("Create() error = %v, want ErrDuplicateKey", err)
		}

		// The driver's own error survives, with the constraint that failed.
		var write mongo.WriteException
		if !errors.As(err, &write) {
			t.Fatal("the driver's WriteException was lost")
		}
		if len(write.WriteErrors) == 0 || write.WriteErrors[0].Code != 11000 {
			t.Errorf("write errors = %v, want MongoDB's code 11000", write.WriteErrors)
		}
	})

	t.Run("CreateMany reports it too", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)
		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("SyncIndexes() error = %v", err)
		}

		err := accounts.CreateMany(ctx, []*account{
			{Email: "a@example.com"},
			{Email: "a@example.com"},
		})
		if !errors.Is(err, odm.ErrDuplicateKey) {
			t.Errorf("CreateMany() error = %v, want ErrDuplicateKey", err)
		}
	})

	t.Run("Update reports it too", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)
		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("SyncIndexes() error = %v", err)
		}

		taken := &account{Email: "taken@example.com"}
		other := &account{Email: "other@example.com"}
		if err := accounts.CreateMany(ctx, []*account{taken, other}); err != nil {
			t.Fatalf("CreateMany() error = %v", err)
		}

		_, err := accounts.Where("_id", other.ID).Set("email", "taken@example.com").Update(ctx)
		if !errors.Is(err, odm.ErrDuplicateKey) {
			t.Errorf("Update() error = %v, want ErrDuplicateKey", err)
		}
	})

	t.Run("an ordinary write is not classified", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		accounts := newAccounts(t)
		if err := accounts.SyncIndexes(ctx); err != nil {
			t.Fatalf("SyncIndexes() error = %v", err)
		}

		if err := accounts.Create(ctx, &account{Email: "fine@example.com"}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	})
}
