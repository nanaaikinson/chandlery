package db

import (
	"context"
	"fmt"
	"reflect"

	"github.com/uptrace/bun"
)

type QueryFn func(*bun.SelectQuery) *bun.SelectQuery
type UpdateQueryFn func(*bun.UpdateQuery) *bun.UpdateQuery

// Repository works with T as either a struct (Repository[Model]) or a
// pointer to one (Repository[*Model]) — every method runs either way. Create
// and Update still mutate the row through bun's model hooks (e.g.
// BeforeAppendModel assigning a ULID) regardless of T's shape, but that
// mutation is only visible to the caller when T is a pointer: Create/Update
// take item by value, so with a non-pointer T the hook mutates the method's
// own local copy, not the caller's variable. Use Repository[*Model] when you
// need to read back a DB-assigned field (ULID, defaulted timestamp) after
// Create.
type Repository[T any] interface {
	GetOne(ctx context.Context, queryFn QueryFn) (T, error)
	GetMany(ctx context.Context, queryFn QueryFn) ([]T, error)
	// Count reports how many rows match queryFn's own filters (ignoring any
	// Limit/Offset it sets) - the real pagination total, run as its own
	// query alongside GetMany's page fetch rather than derived from
	// "did a full page come back."
	Count(ctx context.Context, queryFn QueryFn) (int, error)
	Exists(ctx context.Context, queryFn QueryFn) (bool, error)
	Create(ctx context.Context, item T) error
	Update(ctx context.Context, item T) error
	UpdateByMap(ctx context.Context, values map[string]any, queryFn UpdateQueryFn) error
	Delete(ctx context.Context, item T) error
	// DB returns the underlying connection, for callers that embed a
	// Repository but need to build one custom query (a conditional UPDATE,
	// an ON CONFLICT insert) this interface has no method for.
	DB() bun.IDB
}

type repository[T any] struct {
	// bun.IDB (not *bun.DB) so a repository built from a bun.Tx runs its
	// queries inside that transaction — both satisfy bun.IDB.
	db bun.IDB
}

func NewRepository[T any](db bun.IDB) Repository[T] {
	return &repository[T]{db: db}
}

// newModelTarget returns a fresh, addressable value for bun's Model() to
// scan a row into, which accepts at most one level of pointer indirection.
// When T is itself a pointer (e.g. Repository[*models.User]), &item would be
// a pointer-to-pointer, which bun rejects — so this allocates the
// pointed-to struct and returns item itself. Only for the read paths: it
// discards whatever item already pointed to, which is correct when item is
// about to be overwritten by a Scan, but would silently drop a caller's
// existing field values if reused for insert/update — see modelArg for that
// case.
func newModelTarget[T any](item *T) any {
	if t := reflect.TypeFor[T](); t.Kind() == reflect.Pointer {
		reflect.ValueOf(item).Elem().Set(reflect.New(t.Elem()))
		return *item
	}
	return item
}

// modelArg returns a value to pass to bun's Model() for insert/update/delete,
// preserving item's existing field values — unlike newModelTarget, it never
// allocates a fresh zero value, since the caller's data (e.g. a Create
// payload) must survive the call. When T is a pointer, item already is one
// level of indirection, so it's returned as-is; when T is a plain struct,
// item (itself *T here) is the pointer bun needs.
func modelArg[T any](item *T) any {
	if reflect.TypeFor[T]().Kind() == reflect.Pointer {
		return *item
	}
	return item
}

func (r *repository[T]) DB() bun.IDB {
	return r.db
}

func (r *repository[T]) GetOne(ctx context.Context, queryFn QueryFn) (T, error) {
	var item T
	err := queryFn(r.db.NewSelect().Model(newModelTarget(&item))).Scan(ctx)
	return item, err
}

func (r *repository[T]) GetMany(ctx context.Context, queryFn QueryFn) ([]T, error) {
	var items []T
	err := queryFn(r.db.NewSelect().Model(&items)).Scan(ctx)
	return items, err
}

func (r *repository[T]) Count(ctx context.Context, queryFn QueryFn) (int, error) {
	var item T
	return queryFn(r.db.NewSelect().Model(newModelTarget(&item))).Count(ctx)
}

func (r *repository[T]) Exists(ctx context.Context, queryFn QueryFn) (bool, error) {
	var item T
	return queryFn(r.db.NewSelect().Model(newModelTarget(&item))).Exists(ctx)
}

func (r *repository[T]) Create(ctx context.Context, item T) error {
	_, err := r.db.NewInsert().Model(modelArg(&item)).Exec(ctx)
	return err
}

func (r *repository[T]) Update(ctx context.Context, item T) error {
	_, err := r.db.NewUpdate().Model(modelArg(&item)).WherePK().Exec(ctx)
	return err
}

// UpdateByMap sets specific columns from a map without loading a full model.
// queryFn must scope the target rows, e.g. q.Where("id = ?", id) — nothing
// here verifies that, so an unscoped queryFn updates every row in the table.
// Bypasses bun's model hooks entirely: unlike Update, it does not refresh
// e.g. Model's UpdatedAt on its own — include that column in values yourself
// if you need it bumped.
func (r *repository[T]) UpdateByMap(ctx context.Context, values map[string]any, queryFn UpdateQueryFn) error {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return fmt.Errorf("db: UpdateByMap: T must be a struct or pointer to struct, got %s", reflect.TypeFor[T]())
	}

	q := r.db.NewUpdate()
	// *bun.DB.Table isn't part of bun.IDB, but every query carries a
	// reference back to its underlying *bun.DB via q.DB(), which has it —
	// works whether r.db is the top-level DB or a Tx.
	table := q.DB().Table(t).Name
	_, err := queryFn(q.Model(&values).Table(table)).Exec(ctx)
	return err
}

func (r *repository[T]) Delete(ctx context.Context, item T) error {
	_, err := r.db.NewDelete().Model(modelArg(&item)).WherePK().Exec(ctx)
	return err
}

// ConditionalUpdate runs an UPDATE guarded by queryFn's own WHERE clause and
// reports whether any row matched — the shared shape behind every "flip this
// row's status if it's still in an expected prior state" repository method
// (e.g. PayoutRepository.MarkProcessing, PaymentLinkPaymentRepository.
// ReleaseEscrow), used instead of a plain check-then-write to avoid a race
// between the check and the write. M must be the model type itself, not a
// pointer, e.g. db.ConditionalUpdate[models.Payout](ctx, r.DB(), ...) —
// db.ConditionalUpdate[*models.Payout] is a compile-time-legal but wrong
// instantiation this function rejects at runtime rather than handing bun a
// nil double pointer.
func ConditionalUpdate[M any](ctx context.Context, database bun.IDB, queryFn UpdateQueryFn) (bool, error) {
	if reflect.TypeFor[M]().Kind() == reflect.Pointer {
		return false, fmt.Errorf("db: ConditionalUpdate[%s]: M must be the model type itself, not a pointer", reflect.TypeFor[M]())
	}

	res, err := queryFn(database.NewUpdate().Model((*M)(nil))).Exec(ctx)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}
