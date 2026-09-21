//go:build integration

package odm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nanaaikinson/chandlery/odm"
)

// Single-document writes pick their document with OrderBy. UpdateOne hands
// the sort to the server, which is what makes MongoDB 8.0 this package's
// minimum; DeleteOne can't, because the driver exposes no sort on deleteOne,
// so it goes through findAndModify instead. Both are checked here against a
// real server rather than against which branch the code took.

func TestSortedUpdateOne(t *testing.T) {
	t.Parallel()

	t.Run("updates the document the sort picks", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "second", Status: "pending", Age: 30},
			&user{Name: "first", Status: "pending", Age: 10},
			&user{Name: "third", Status: "pending", Age: 50},
		)

		result, err := users.
			Where("status", "pending").
			OrderBy("age", odm.Asc).
			Set("status", "processing").
			UpdateOne(ctx)
		if err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			t.Errorf("UpdateOne() matched/modified = %d/%d, want 1/1", result.MatchedCount, result.ModifiedCount)
		}

		got, err := users.Where("status", "processing").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "first" {
			t.Errorf("UpdateOne() updated %q, want the lowest age, %q", got.Name, "first")
		}
	})

	t.Run("the opposite sort picks the other end", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "oldest", Status: "pending", Age: 10},
			&user{Name: "newest", Status: "pending", Age: 50},
		)

		if _, err := users.
			Where("status", "pending").
			OrderBy("age", odm.Desc).
			Set("status", "processing").
			UpdateOne(ctx); err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}

		got, err := users.Where("status", "processing").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "newest" {
			t.Errorf("UpdateOne() updated %q, want the highest age, %q", got.Name, "newest")
		}
	})

	t.Run("claims the oldest document via Oldest", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)

		// Create keeps a caller-supplied CreatedAt, so the two documents
		// get an explicit order to sort on.
		first := &user{Name: "first", Status: "pending"}
		first.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		second := &user{Name: "second", Status: "pending"}
		second.CreatedAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		seed(t, users, second, first)

		if _, err := users.
			Where("status", "pending").
			Oldest().
			Set("status", "processing").
			UpdateOne(ctx); err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}

		got, err := users.Where("status", "processing").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "first" {
			t.Errorf("Oldest() claimed %q, want %q", got.Name, "first")
		}
	})

	t.Run("reports no match without erroring", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "a", Status: "active"})

		result, err := users.
			Where("status", "pending").
			OrderBy("age", odm.Asc).
			Set("status", "processing").
			UpdateOne(ctx)
		if err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}
		if result.MatchedCount != 0 || result.ModifiedCount != 0 {
			t.Errorf("UpdateOne() matched/modified = %d/%d, want 0/0", result.MatchedCount, result.ModifiedCount)
		}
	})

	t.Run("reports an exact ModifiedCount for a no-op", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "a", Status: "pending"})

		// Setting a field to the value it already holds matches but changes
		// nothing, and the server says so exactly. WithoutTimestamps keeps
		// it a true no-op — the automatic updated_at would otherwise be a
		// real change.
		//
		// This is also the regression guard on the sort reaching the server
		// natively: routed through findAndModify instead, the count could
		// only be a guess, and this would read 1/1.
		result, err := users.
			Where("status", "pending").
			OrderBy("name", odm.Asc).
			Set("status", "pending").
			WithoutTimestamps().
			UpdateOne(ctx)
		if err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 0 {
			t.Errorf("UpdateOne() matched/modified = %d/%d, want 1/0", result.MatchedCount, result.ModifiedCount)
		}
	})

	t.Run("an unsorted UpdateOne behaves the same way", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "a", Status: "pending"})

		result, err := users.
			Where("status", "pending").
			Set("status", "pending").
			WithoutTimestamps().
			UpdateOne(ctx)
		if err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 0 {
			t.Errorf("UpdateOne() matched/modified = %d/%d, want 1/0", result.MatchedCount, result.ModifiedCount)
		}
	})
}

func TestSortedDeleteOne(t *testing.T) {
	t.Parallel()

	t.Run("deletes the document the sort picks", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		doomed := &user{Name: "first", Status: "stale", Age: 10}
		seed(t, users,
			doomed,
			&user{Name: "second", Status: "stale", Age: 30},
		)

		result, err := users.
			Where("status", "stale").
			OrderBy("age", odm.Asc).
			DeleteOne(ctx)
		if err != nil {
			t.Fatalf("DeleteOne() error = %v", err)
		}
		if result.DeletedCount != 1 {
			t.Errorf("DeleteOne() deleted = %d, want 1", result.DeletedCount)
		}

		if _, err := users.Find(ctx, doomed.ID); !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("Find() error = %v, want ErrModelNotFound for the deleted document", err)
		}

		survivor, err := users.First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if survivor.Name != "second" {
			t.Errorf("survivor = %q, want %q", survivor.Name, "second")
		}
	})

	t.Run("reports no match without erroring", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "a", Status: "active"})

		result, err := users.
			Where("status", "stale").
			OrderBy("age", odm.Asc).
			DeleteOne(ctx)
		if err != nil {
			t.Fatalf("DeleteOne() error = %v", err)
		}
		if result.DeletedCount != 0 {
			t.Errorf("DeleteOne() deleted = %d, want 0", result.DeletedCount)
		}
	})
}
