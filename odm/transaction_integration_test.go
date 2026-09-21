//go:build integration

package odm_test

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/nanaaikinson/chandlery/odm"
)

func TestTransaction(t *testing.T) {
	t.Parallel()

	t.Run("commits everything the callback wrote", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		users := odm.Use[user](database)

		err := database.Transaction(ctx, func(ctx context.Context) error {
			if err := users.Create(ctx, &user{Name: "a"}); err != nil {
				return err
			}
			return users.Create(ctx, &user{Name: "b"})
		})
		if err != nil {
			t.Fatalf("Transaction() error = %v", err)
		}

		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 2 {
			t.Errorf("Count() = %d, want 2", count)
		}
	})

	t.Run("rolls everything back when the callback fails", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		users := odm.Use[user](database)

		sentinel := errors.New("second thoughts")
		err := database.Transaction(ctx, func(ctx context.Context) error {
			if err := users.Create(ctx, &user{Name: "a"}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("Transaction() error = %v, want the callback's own error", err)
		}

		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 0 {
			t.Errorf("Count() = %d, want 0 — the write should have rolled back", count)
		}
	})

	t.Run("updates and deletes take part too", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		users := odm.Use[user](database)

		kept := &user{Name: "kept", Status: "pending"}
		doomed := &user{Name: "doomed"}
		seed(t, users, kept, doomed)

		sentinel := errors.New("abort")
		err := database.Transaction(ctx, func(ctx context.Context) error {
			if _, err := users.Where("_id", kept.ID).Set("status", "active").Update(ctx); err != nil {
				return err
			}
			if _, err := users.Where("_id", doomed.ID).Delete(ctx); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("Transaction() error = %v", err)
		}

		got, err := users.Find(ctx, kept.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.Status != "pending" {
			t.Errorf("status = %q, want the update rolled back to %q", got.Status, "pending")
		}
		if _, err := users.Find(ctx, doomed.ID); err != nil {
			t.Errorf("Find() error = %v, want the delete rolled back", err)
		}
	})

	t.Run("reads inside see the transaction's own writes", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		users := odm.Use[user](database)

		err := database.Transaction(ctx, func(ctx context.Context) error {
			if err := users.Create(ctx, &user{Name: "a"}); err != nil {
				return err
			}

			count, err := users.Count(ctx)
			if err != nil {
				return err
			}
			if count != 1 {
				t.Errorf("Count() inside the transaction = %d, want 1", count)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Transaction() error = %v", err)
		}
	})

	t.Run("hands the callback a context carrying the session", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))

		outer := mongo.SessionFromContext(ctx)
		if outer != nil {
			t.Fatal("the outer context already carries a session")
		}

		err := database.Transaction(ctx, func(ctx context.Context) error {
			if mongo.SessionFromContext(ctx) == nil {
				t.Error("the callback's context carries no session — writes would escape the transaction")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Transaction() error = %v", err)
		}
	})

	t.Run("a write on the outer context escapes the transaction", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		database := odm.New(testDatabase(t, client))
		users := odm.Use[user](database)

		sentinel := errors.New("abort")
		err := database.Transaction(ctx, func(_ context.Context) error {
			// Deliberately the wrong context — the documented foot-gun.
			if err := users.Create(ctx, &user{Name: "escaped"}); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("Transaction() error = %v", err)
		}

		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1 — a write on the outer context commits on its own", count)
		}
	})
}
