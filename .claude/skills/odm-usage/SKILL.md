---
name: odm-usage
description: >
  Use when writing or reviewing Go application code that persists data with
  github.com/nanaaikinson/chandlery/odm — defining models, querying, updating,
  soft deletes, scopes, hooks, observers, cursor pagination, aggregation,
  relationships, transactions, indexes, and testing models. For changing the
  odm package itself, use chandlery-odm instead.
---

# Using the Chandlery ODM

This skill is for **consuming** `github.com/nanaaikinson/chandlery/odm` in an
application. Extending the package itself is a different job — see the
`chandlery-odm` skill.

Full reference: `odm/GUIDE.md` and `odm/README.md` in the chandlery repo, or
`go doc github.com/nanaaikinson/chandlery/odm`.

Requires **MongoDB 8.0+**.

---

## Before writing code

1. Read the model's struct definition. What it embeds decides half the
   behavior — whether `Delete` removes or stamps, whether `Save` works,
   whether timestamps happen.
2. Check for existing scopes, relations and observers in the package. Reuse
   them; they usually encode tenant scoping or business rules.
3. Never invent an API. If unsure a method exists, check `go doc`. This
   package has near-misses that read plausibly but do not exist (see
   **Does not exist** below).

---

## The five things most likely to be wrong

Check these in any code you write or review.

### 1. `bson:",inline"` on embedded bases

```go
type User struct {
    odm.Model `bson:",inline"`   // REQUIRED
    Name string `bson:"name"`
}
```

Without the tag the embedded struct nests under a lowercased `model` key, so
the document has **no top-level `_id`** — and every query still compiles.
This is the single most damaging mistake available.

### 2. Relation fields need `bson:"-"`

```go
Orders []Order `bson:"-"`   // loaded by With, never stored
```

Leave it off and a copy of every related document is persisted inside the
parent.

### 3. `OrWhere` is not SQL precedence

Folded strictly left to right:

```go
q.Where(a).OrWhere(b).Where(c)   // (a OR b) AND c   — NOT a OR (b AND c)
```

For a grouped predicate, write the `$or` with `WhereRaw`.

### 4. Transactions: use the callback's `ctx`

```go
database.Transaction(ctx, func(ctx context.Context) error {
    return users.Create(ctx, &user)   // this ctx, never the outer one
})
```

A call using the outer `ctx` runs **outside** the transaction and commits on
its own, silently. The callback may also run more than once (driver retries),
so keep it idempotent and keep non-database work out of it.

### 5. `Update`/`Delete` are the Many variants

An unfiltered query hits the whole collection. They live on `Query`, not
`Collection`, so clearing everything requires an explicit
`users.Query().Delete(ctx)`.

---

## Choosing the right write

| Situation | Use |
| --- | --- |
| New document | `Create(ctx, &model)` |
| Several new documents, one round trip | `CreateMany(ctx, []*T{...})` |
| Patch a model you loaded | `Save(ctx, &model)` — writes only what changed |
| Change fields without loading | `Where(...).Set(...).Update(ctx)` |
| Change one document, chosen by order | `OrderBy(...).Set(...).UpdateOne(ctx)` |
| Counter, array, or anything atomic | `Inc` / `Push` / `Pull` / `AddToSet` |
| Operator this package doesn't wrap | `UpdateRaw(ctx, bson.M{...})` |
| Batch of mixed operations | `BulkWrite(ctx, []mongo.WriteModel{...})` |

**Prefer atomic operators over read-modify-write.** Do not load a model,
increment a field in Go, and `Save` — use `Inc`. It is one round trip and
cannot lose a concurrent update.

**Use a guard clause for state transitions:**

```go
result, err := invoices.
    Where("_id", id).
    Where("status", "pending").      // the guard
    Set("status", "paid").
    Update(ctx)
if result.MatchedCount == 0 {
    return ErrAlreadySettled          // someone else got there first
}
```

---

## Querying

Conditions AND together, including two on the same field. Operators:
`=`, `==`, `!=`, `>`, `>=`, `<`, `<=`, or the typed `odm.Gt`, `odm.Gte`,
`odm.Lt`, `odm.Lte`, `odm.Eq`, `odm.Ne` (compile-checked; prefer these in
code that will be maintained).

```go
users.
    Where("business_id", id).
    Where("age", odm.Gte, 18).
    WhereIn("status", []string{"active", "pending"}).
    WhereNotNull("email").
    OrderBy("created_at", odm.Desc).
    Select("name", "email").
    Limit(20).
    Get(ctx)
```

Terminals: `Get` (`[]T`), `First` (`T`, `ErrModelNotFound` when missing),
`Find(ctx, id)`, `Count` (int64, ignores Limit/Skip), `Exists` (bool).

**Queries are immutable.** Branch freely from a base query; nothing
interferes.

**MongoDB semantics, not SQL:**

- `WhereNull(f)` matches null **and missing**. For present-and-null use
  `WhereRaw(bson.M{f: bson.M{"$type": "null"}})`.
- `WhereNotIn(f, []T{})` with an empty slice matches **everything**
  (`$nin: []`). Guard the empty case.
- `WhereNotBetween` requires the field to exist.

---

## Scopes for tenant isolation

This is the highest-value pattern in a multi-tenant app. Define once:

```go
func ForBusiness(id string) odm.Scope[Customer] {
    return func(q *odm.Query[Customer]) *odm.Query[Customer] {
        return q.Where("business_id", id)
    }
}

func (a *App) customersFor(id string) *odm.Query[Customer] {
    return a.customers.Scope(ForBusiness(id), Active)
}
```

Then every downstream read is scoped by construction rather than by everyone
remembering. A scope cannot perform I/O — no context, no error return.

---

## Soft deletes

If the model embeds `odm.SoftDeletes`, `Delete` stamps `deleted_at` and all
reads skip those documents.

```go
users.Get(ctx)                            // live only
users.WithTrashed().Get(ctx)              // live and deleted
users.OnlyTrashed().Get(ctx)              // deleted only
users.Where("_id", id).Restore(ctx)       // clear the stamp
users.Where("_id", id).ForceDelete(ctx)   // remove for real
```

- `ForceDelete` **ignores** the default scope (purging is the point).
- `Restore` only ever touches trashed documents.
- These four error with `ErrInvalidQuery` on a model without `SoftDeletes`.

---

## Relationships

Declare once, at package level, next to the models:

```go
var UserOrders = odm.HasMany[User, Order]{
    ForeignKey: "user_id",
    Attach:     func(u *User, o []Order) { u.Orders = o },
}
```

| Declaration | Key lives on | Attach gets |
| --- | --- | --- |
| `HasMany[T,R]` | related model | `[]R` |
| `HasOne[T,R]` | related model | `*R` (nil when absent) |
| `BelongsTo[T,R]` | this model | `*R` (nil when absent/dangling) |
| `BelongsToMany[T,R]` | this model, as a list | `[]R` |

Load with `With`; nest with `Relation.With`:

```go
users.With(UserOrders).Get(ctx)
users.With(UserOrders.With(OrderPayments)).Get(ctx)   // 3 queries, 3 levels
```

**Loading always batches** — one query per relation per level, never one per
parent. If you find yourself looping over results and querying inside the
loop, you want `With` instead.

**Many-to-many is a list of ids, not a join collection.** Use
`BelongsToMany` where the list is on this model, `HasMany` where it is on the
other. A join collection carrying its own fields is a model of its own with
two `BelongsTo`.

**Nothing loads on field access.** Reading `user.Orders` reads a struct field.

---

## Pagination

Use `CursorPaginate` for anything user-facing. `Skip` is for small offsets
only.

```go
page, err := users.
    OrderBy("created_at", odm.Desc).
    CursorPaginate(ctx, odm.CursorPagination{Limit: 20, Cursor: c})
// page.Data, page.NextCursor, page.HasMore — already JSON-shaped
```

Handle `ErrInvalidCursor` as a 400: cursors arrive from URLs and go stale.
A projection must keep every field the sort uses.

---

## Errors

Always `errors.Is`, never string matching.

| Sentinel | When |
| --- | --- |
| `ErrModelNotFound` | `First`/`Find` matched nothing |
| `ErrDuplicateKey` | unique index violated |
| `ErrInvalidCursor` | bad or stale pagination cursor |
| `ErrInvalidQuery` | invalid builder input (surfaces at the terminal) |
| `ErrNilModel` | nil pointer to `Create`/`Save` |

The driver's error stays wrapped, so `errors.As` still reaches
`*mongo.WriteException` for the constraint name.

---

## Testing

```go
// Validate a model wires up correctly — catches missing bson:",inline",
// duplicate tags, relation fields that get persisted.
odm.TestConformance(t, odm.Use[User](database), func() *User {
    return &User{Name: "Nana", Email: "nana@example.com"}
})

// Freeze the clock so timestamps are assertable.
database := odm.New(raw, odm.WithClock(func() time.Time { return fixed }))
```

Use a real MongoDB (testcontainers). The behaviours this package leans on —
null vs missing, `$nin` on empty, index conflicts, transaction rollback — are
exactly what a fake gets wrong.

---

## Does not exist

Do not write these. They read plausibly and will not compile:

| Wrong | Right |
| --- | --- |
| `odm.IsDirtyOrPanic(m, f)` | `odm.IsDirty(m, f)` → `(bool, error)` |
| `odm.Exists(&model)` | `odm.IsPersisted(&model)` — `Exists` is the query terminal |
| `q.WhereLike(...)`, `q.WhereNot(...)` | `WhereRaw` |
| `q.OrWhereIn(...)`, `q.OrWhereNull(...)` | only `OrWhere` exists |
| `q.Paginate(page, perPage)` | `CursorPaginate`, or `Limit`+`Skip` |
| `q.Pluck(...)`, `q.Sum(...)`, `q.Max(...)` | `Select` + `Get`, or `AggregateInto` |
| `collection.Update(ctx)` | mutating terminals are on `Query` only |
| `odm.UseTx[T](tx)`, `*odm.Tx` | pass the transaction's `ctx` to existing collections |
| `model.Save(ctx)` | `collection.Save(ctx, &model)` |
| polymorphic / morphTo relations | not supported; use two typed relations |

`Changes` returns `(odm.Changeset, error)` — a struct with `Set bson.M` and
`Unset []string`, not a bare map.

---

## Review checklist

- `bson:",inline"` on every embedded `odm.Model` / `odm.IdentityModel` /
  `odm.SoftDeletes`?
- `bson:"-"` on every relation field?
- Tenant scoping applied — by scope, not by hand at each call site?
- Read-modify-write where an atomic operator would do?
- Loop containing a query, where `With` would batch?
- Transaction callback using its own `ctx`? Idempotent? No mail/payments
  inside?
- `errors.Is` rather than string matching?
- Unfiltered `Update`/`Delete` that should have been scoped?
- `ErrInvalidCursor` handled on paginated endpoints?
- `SyncIndexes` called at startup for models declaring `Indexes()`?
