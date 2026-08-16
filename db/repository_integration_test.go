//go:build integration

package db_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/uptrace/bun"

	"github.com/nanaaikinson/chandlery/db"
)

type item struct {
	bun.BaseModel `bun:"table:items,alias:i"`
	db.Model

	Name  string `bun:"name,notnull"`
	Price int    `bun:"price,notnull"`
}

// newTx opens a transaction against testDB and rolls it back on cleanup, so
// each test gets its own isolated view of the (shared, parallel-run) items
// table instead of racing other tests over a table-wide DELETE.
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

// newItemRepo returns a Repository[*item] scoped to its own transaction:
// pointer T, so Create/Update mutate the caller's own variable (e.g.
// assigning its ULID) instead of a throwaway local copy. TestRepository_Create
// separately exercises value T.
func newItemRepo(t *testing.T) db.Repository[*item] {
	t.Helper()
	return db.NewRepository[*item](newTx(t))
}

func TestRepository_Create(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	repo := db.NewRepository[item](newTx(t))

	// Repository[item] (value T): Create takes item by value, so the ID/
	// timestamps BeforeAppendModel assigns land on Create's own local copy,
	// not the caller's variable - the insert itself must still succeed
	// though (this used to crash bun with "Model(non-pointer ...)"), and the
	// assigned fields are visible by reading the row back.
	if err := repo.Create(ctx, item{Name: "widget", Price: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("name = ?", "widget")
	})
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if got.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if _, err := ulid.ParseStrict(got.ID); err != nil {
		t.Errorf("assigned ID %q is not a valid ULID: %v", got.ID, err)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("CreatedAt/UpdatedAt not stamped: %+v", got)
	}
}

func TestRepository_Create_PointerT(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	repo := db.NewRepository[*item](newTx(t))

	// Repository[*item] (pointer T): item is already the pointer Create
	// mutates, so BeforeAppendModel's ID/timestamp assignment is visible on
	// the caller's own variable immediately - no round trip needed.
	it := &item{Name: "gizmo", Price: 50}
	if err := repo.Create(ctx, it); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if it.ID == "" {
		t.Fatal("Create did not assign an ID to the caller's variable")
	}
	if _, err := ulid.ParseStrict(it.ID); err != nil {
		t.Errorf("assigned ID %q is not a valid ULID: %v", it.ID, err)
	}
}

func TestRepository_GetOneAndExists(t *testing.T) {
	t.Parallel()
	repo := newItemRepo(t)
	ctx := t.Context()

	seed := &item{Name: "gadget", Price: 250}
	if err := repo.Create(ctx, seed); err != nil {
		t.Fatalf("Create: %v", err)
	}

	byName := func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("name = ?", "gadget")
	}

	got, err := repo.GetOne(ctx, byName)
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if got.Price != 250 {
		t.Errorf("Price = %d, want %d", got.Price, 250)
	}

	exists, err := repo.Exists(ctx, byName)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Error("Exists(matching filter) = false, want true")
	}

	missing, err := repo.Exists(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("name = ?", "does-not-exist")
	})
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if missing {
		t.Error("Exists(non-matching filter) = true, want false")
	}
}

func TestRepository_GetManyAndCount(t *testing.T) {
	t.Parallel()
	repo := newItemRepo(t)
	ctx := t.Context()

	for i := range 3 {
		if err := repo.Create(ctx, &item{Name: "batch", Price: i}); err != nil {
			t.Fatalf("seeding item %d: %v", i, err)
		}
	}

	byBatch := func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("name = ?", "batch")
	}

	// Count must ignore GetMany's own Limit and report the real total.
	count, err := repo.Count(ctx, byBatch)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Errorf("Count = %d, want 3", count)
	}

	page, err := repo.GetMany(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return byBatch(q).Limit(2)
	})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("len(GetMany with Limit(2)) = %d, want 2", len(page))
	}
}

func TestRepository_Update(t *testing.T) {
	t.Parallel()
	repo := newItemRepo(t)
	ctx := t.Context()

	it := &item{Name: "widget", Price: 100}
	if err := repo.Create(ctx, it); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Compare against CreatedAt as Postgres actually stored it, not the
	// in-memory pre-insert value: Postgres timestamptz only has microsecond
	// precision, so an in-memory time.Time (nanosecond precision) essentially
	// never round-trips as Equal - it only happened to pass locally because
	// the local clock's sub-microsecond bits were often already zero, and
	// failed on CI's Linux runners where they weren't.
	before, err := repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", it.ID)
	})
	if err != nil {
		t.Fatalf("GetOne (before update): %v", err)
	}

	it.Price = 200
	if err := repo.Update(ctx, it); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", it.ID)
	})
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if got.Price != 200 {
		t.Errorf("Price = %d, want %d", got.Price, 200)
	}
	if !got.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("CreatedAt changed on Update: got %v, want %v", got.CreatedAt, before.CreatedAt)
	}
}

func TestRepository_UpdateByMap(t *testing.T) {
	t.Parallel()
	repo := newItemRepo(t)
	ctx := t.Context()

	it := &item{Name: "widget", Price: 100}
	if err := repo.Create(ctx, it); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := repo.UpdateByMap(ctx, map[string]any{"price": 999}, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Where("id = ?", it.ID)
	})
	if err != nil {
		t.Fatalf("UpdateByMap: %v", err)
	}

	got, err := repo.GetOne(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", it.ID)
	})
	if err != nil {
		t.Fatalf("GetOne: %v", err)
	}
	if got.Price != 999 {
		t.Errorf("Price = %d, want %d", got.Price, 999)
	}
}

func TestRepository_UpdateByMap_RejectsUnsupportedT(t *testing.T) {
	t.Parallel()
	tx := newTx(t)

	// Repository[string]: T isn't a struct or pointer-to-struct. UpdateByMap
	// used to panic deep inside bun for this shape instead of erroring
	// cleanly like every other Repository method already does.
	repo := db.NewRepository[string](tx)

	err := repo.UpdateByMap(t.Context(), map[string]any{"x": 1}, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Where("1=1")
	})
	if err == nil {
		t.Fatal("UpdateByMap with unsupported T = nil error, want a clear error instead of a panic")
	}
}

func TestRepository_Delete(t *testing.T) {
	t.Parallel()
	repo := newItemRepo(t)
	ctx := t.Context()

	it := &item{Name: "widget", Price: 100}
	if err := repo.Create(ctx, it); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, it); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	exists, err := repo.Exists(ctx, func(q *bun.SelectQuery) *bun.SelectQuery {
		return q.Where("id = ?", it.ID)
	})
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Error("Exists after Delete = true, want false")
	}
}

func TestConditionalUpdate(t *testing.T) {
	t.Parallel()
	tx := newTx(t)
	repo := db.NewRepository[*item](tx)
	ctx := t.Context()

	it := &item{Name: "pending", Price: 0}
	if err := repo.Create(ctx, it); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Guard matches the row's actual state ("pending") - update applies.
	applied, err := db.ConditionalUpdate[item](ctx, tx, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("name = ?", "processing").Where("id = ? and name = ?", it.ID, "pending")
	})
	if err != nil {
		t.Fatalf("ConditionalUpdate: %v", err)
	}
	if !applied {
		t.Fatal("ConditionalUpdate with a matching guard reported no row affected")
	}

	// Guard no longer matches (row is now "processing", not "pending") -
	// update must not silently re-apply.
	appliedAgain, err := db.ConditionalUpdate[item](ctx, tx, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("name = ?", "processing").Where("id = ? and name = ?", it.ID, "pending")
	})
	if err != nil {
		t.Fatalf("ConditionalUpdate: %v", err)
	}
	if appliedAgain {
		t.Error("ConditionalUpdate with a stale guard reported a row affected, want none")
	}
}

func TestConditionalUpdate_RejectsPointerM(t *testing.T) {
	t.Parallel()
	tx := newTx(t)

	// db.ConditionalUpdate[*item] is a compile-time-legal but wrong
	// instantiation (M must be the model type itself) — it used to reach
	// bun as a nil double pointer and fail with a confusing error; it should
	// now be rejected clearly before ever building the query.
	_, err := db.ConditionalUpdate[*item](t.Context(), tx, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("name = ?", "x").Where("1=1")
	})
	if err == nil {
		t.Fatal("ConditionalUpdate[*item] = nil error, want a clear rejection of the pointer type param")
	}
}
