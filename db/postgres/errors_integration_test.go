//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/uptrace/bun"

	"github.com/nanaaikinson/chandlery/db/postgres"
)

// TestUniqueViolation and TestForeignKeyViolation exist because a bug was
// found writing this suite: ForeignKeyViolation used to check SQLSTATE
// 23001 (restrict_violation), which Postgres never actually raises for FK
// enforcement — verified empirically against real Postgres 16, both an
// ON DELETE RESTRICT-blocked delete and an insert against a missing parent
// row raise 23503 (foreign_key_violation). These pin that real behavior down
// so it can't silently regress.

// newTx opens a transaction against testDB and rolls it back on cleanup, so
// each test gets its own isolated view of the (shared, parallel-run)
// accounts/categories/products tables instead of leaving rows behind for
// other tests or later runs to collide with.
func newTx(t *testing.T) bun.Tx {
	t.Helper()
	// context.Background(), not t.Context(): t.Context() is canceled right
	// before Cleanup runs, and database/sql auto-rolls-back a Tx whose
	// governing context is canceled — racing against the explicit
	// Rollback() below and intermittently failing it with
	// "transaction has already been committed or rolled back". The tx's own
	// lifetime is controlled entirely by this helper, not by the test's
	// context.
	tx, err := testDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("beginning transaction: %v", err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(); err != nil {
			t.Fatalf("rolling back transaction: %v", err)
		}
	})
	return tx
}

func TestUniqueViolation(t *testing.T) {
	t.Parallel()
	tx := newTx(t)
	ctx := t.Context()

	_, err := tx.ExecContext(ctx, `insert into accounts (id, email) values ('acct-1', 'dup@example.com')`)
	if err != nil {
		t.Fatalf("seeding first account: %v", err)
	}

	_, err = tx.ExecContext(ctx, `insert into accounts (id, email) values ('acct-2', 'dup@example.com')`)
	if err == nil {
		t.Fatal("expected a unique-violation error inserting a duplicate email, got nil")
	}

	constraint, ok := postgres.UniqueViolation(err)
	if !ok {
		t.Fatalf("UniqueViolation(%v) = false, want true", err)
	}
	if constraint != "accounts_email_key" {
		t.Errorf("constraint = %q, want %q", constraint, "accounts_email_key")
	}
}

func TestForeignKeyViolation_BlockedDelete(t *testing.T) {
	t.Parallel()
	tx := newTx(t)
	ctx := t.Context()

	if _, err := tx.ExecContext(ctx, `insert into categories (id) values ('cat-restrict')`); err != nil {
		t.Fatalf("seeding category: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `insert into products (id, category_id) values ('prod-1', 'cat-restrict')`); err != nil {
		t.Fatalf("seeding product: %v", err)
	}

	_, err := tx.ExecContext(ctx, `delete from categories where id = 'cat-restrict'`)
	if err == nil {
		t.Fatal("expected a foreign-key-violation error deleting a still-referenced category, got nil")
	}

	constraint, ok := postgres.ForeignKeyViolation(err)
	if !ok {
		t.Fatalf("ForeignKeyViolation(%v) = false, want true", err)
	}
	if constraint != "products_category_id_fkey" {
		t.Errorf("constraint = %q, want %q", constraint, "products_category_id_fkey")
	}
}

func TestForeignKeyViolation_MissingParentOnInsert(t *testing.T) {
	t.Parallel()
	tx := newTx(t)
	ctx := t.Context()

	_, err := tx.ExecContext(ctx, `insert into products (id, category_id) values ('prod-orphan', 'does-not-exist')`)
	if err == nil {
		t.Fatal("expected a foreign-key-violation error inserting against a missing category, got nil")
	}

	constraint, ok := postgres.ForeignKeyViolation(err)
	if !ok {
		t.Fatalf("ForeignKeyViolation(%v) = false, want true", err)
	}
	if constraint != "products_category_id_fkey" {
		t.Errorf("constraint = %q, want %q", constraint, "products_category_id_fkey")
	}
}
