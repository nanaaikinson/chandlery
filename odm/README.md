# odm

A small MongoDB object-document mapper over
[mongo-driver/v2](https://go.mongodb.org/mongo-driver/v2): a generic,
immutable query builder shaped after Laravel Eloquent's developer
experience, staying native MongoDB underneath.

Filters are plain `bson`, sorts are plain `bson`, and every layer hands back
the driver's own types — so nothing here can trap you when you need
something the builder doesn't cover.

This package is independent of [`db`](../db): the two share no types and
neither imports the other.

**New here? Read [GUIDE.md](GUIDE.md)** — every feature worked through one
application, in the order you'd meet it. This file is the reference.

```
go get github.com/nanaaikinson/chandlery
```

Requires Go 1.26.3+, [mongo-driver/v2](https://go.mongodb.org/mongo-driver/v2),
and **MongoDB 8.0 or later** — a sorted `UpdateOne` hands its sort to the
server, and earlier versions reject the field.

## A model

```go
type User struct {
	odm.Model `bson:",inline"`

	Name     string `bson:"name"`
	Email    string `bson:"email"`
	IsActive bool   `bson:"is_active"`
}

func (User) CollectionName() string {
	return "users"
}
```

`odm.Model` is optional. Embed it (inline, so its fields land at the top
level of the document) for a ULID `_id` plus `created_at`/`updated_at` that
`Create` stamps and `Update` refreshes. Two alternatives:

- `odm.IdentityModel` — the ULID `_id` alone, for a collection with no
  timestamps.
- neither — a struct with its own `bson:"_id"`, or none at all, letting
  MongoDB generate an ObjectID.

Embedding `odm.SoftDeletes` alongside opts the model into soft deletion.

`CollectionName` is also optional. Without it the collection name is the
lowercased type name plus `"s"` (`User` → `users`). That is the whole of the
pluralization: a model whose plural isn't its name plus `"s"` (`Person` →
`persons`, `Company` → `companys`) should spell it out.

## Usage

```go
database := odm.New(client.Database("app"))
users := odm.Use[User](database)

user, err := users.
	Where("email", email).
	Where("is_active", true).
	First(ctx)
if errors.Is(err, odm.ErrModelNotFound) {
	// no such user
}
```

`odm.New` takes an already-connected `*mongo.Database`: it never creates or
connects a client, and never closes one. That lifecycle stays yours.

```go
result, err := users.
	Where("business_id", businessID).
	OrderBy("created_at", odm.Desc).
	Limit(20).
	Get(ctx)
```

```go
user := User{Name: "Nana", Email: "nana@example.com"}

// Create takes a pointer, so the ULID and timestamps it assigns are
// visible on your own variable afterwards.
err := users.Create(ctx, &user)
```

### Reading

| Method                            | Notes                                                                                                          |
| --------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `Where(field, value)`             | Equality. Repeated calls AND together.                                                                          |
| `Where(field, operator, value)`    | `odm.Gt`, `odm.Gte`, `odm.Lt`, `odm.Lte`, `odm.Eq`, `odm.Ne` — or the string spelling. Anything else is an error. |
| `OrWhere(field, [operator,] value)` | ORs with everything accumulated so far — see below.                                                           |
| `WhereIn(field, values)`          | `$in`. `values` must be a slice or array.                                                                       |
| `WhereNotIn(field, values)`       | `$nin`. Note an empty slice matches *everything*.                                                               |
| `WhereNull(field)`                | `{field: null}` — matches null **and missing**.                                                                 |
| `WhereNotNull(field)`             | `{field: {$ne: null}}` — present and non-null.                                                                  |
| `WhereBetween(field, from, to)`   | Inclusive `$gte`/`$lte`.                                                                                        |
| `WhereNotBetween(field, from, to)` | `$or` of `$lt`/`$gt`; needs the field to exist.                                                                |
| `WhereRaw(filter)`                | A driver-native BSON document (`bson.M`, `bson.D`, `bson.Raw`, or a tagged struct), ANDed with everything else. |
| `Scope(scopes...)`                | Applies reusable query transformations.                                                                         |
| `With(relations...)`              | Eager-loads relations, one extra query each.                                                                     |
| `WithTrashed()` / `OnlyTrashed()` | Soft-delete models only — see below.                                                                            |
| `Select(fields...)`               | Inclusion projection.                                                                                           |
| `Exclude(fields...)`              | Exclusion projection.                                                                                           |
| `OrderBy(field, direction)`       | `odm.Asc` / `odm.Desc`. Repeated calls break earlier ties.                                                      |
| `Latest([fields...])`             | Descending, `created_at` by default.                                                                            |
| `Oldest([fields...])`             | Ascending, `created_at` by default.                                                                             |
| `Limit(n)` / `Skip(n)`            | `int64`. Negative values are rejected (see below).                                                              |
| `Get(ctx)`                        | `([]T, error)`. Empty result is not an error.                                                                   |
| `First(ctx)`                      | `(T, error)`. Missing → `odm.ErrModelNotFound`.                                                                 |
| `Find(ctx, id)`                   | By `_id`, of whatever type your model uses.                                                                     |
| `Count(ctx)`                      | `(int64, error)`. Ignores `Limit`/`Skip` — the real total.                                                       |
| `Exists(ctx)`                     | `(bool, error)`. Stops at the first match; decodes nothing.                                                      |
| `CursorPaginate(ctx, opts)`       | One seek-based page → `CursorPage[T]`.                                                                           |
| `Aggregate(ctx, pipeline)`        | A native `mongo.Pipeline`, decoded into `T`.                                                                     |
| `Create(ctx, &model)`             | On the collection, not the query.                                                                               |
| `CreateMany(ctx, models)`         | On the collection. One insert, same preparation per model.                                                       |
| `Save(ctx, &model)`               | On the collection. Inserts a new model, updates a changed one.                                                    |
| `BulkWrite(ctx, models)`          | On the collection. Native `[]mongo.WriteModel`, passed straight through.                                          |
| `SyncIndexes(ctx)`                | On the collection. Creates what the model declares.                                                              |

Every one of these can also start a chain directly from the collection
(`users.Where(...)`, `users.Get(ctx)`, ...).

### Writing

| Method                     | Notes                                                              |
| -------------------------- | ------------------------------------------------------------------ |
| `Set(field, value)`        | `$set`                                                              |
| `Unset(field)`             | `$unset` — removes the field, which isn't the same as nulling it.   |
| `Inc(field, value)`        | `$inc`, with any numeric value.                                     |
| `Increment(field, by)`     | `Inc` with an `int64`.                                              |
| `Decrement(field, by)`     | `Inc` with a negated `int64`.                                       |
| `Push(field, value)`       | `$push` — appends one element.                                      |
| `Pull(field, value)`       | `$pull`                                                             |
| `AddToSet(field, value)`   | `$addToSet`                                                         |
| `Update(ctx)`              | `UpdateMany` of the staged operators → `*mongo.UpdateResult`.       |
| `UpdateOne(ctx)`           | Same, against one document — `OrderBy` picks which.                 |
| `UpdateRaw(ctx, update)`   | A native update document (or pipeline), `UpdateMany` semantics.     |
| `WithoutTimestamps()`      | Suppresses the automatic `updated_at` refresh.                       |
| `Delete(ctx)`              | `DeleteMany` → `*mongo.DeleteResult`, or a soft delete — see below.  |
| `DeleteOne(ctx)`           | Same, against one document — `OrderBy` picks which.                  |
| `Restore(ctx)`             | Soft-delete models only. Clears `deleted_at`.                        |
| `ForceDelete(ctx)`         | Soft-delete models only. **Physical delete.**                        |

These live on `Query`, not on `Collection`: they act on every document the
filter matches, so rewriting or emptying a whole collection takes an
explicit `users.Query().Delete(ctx)` rather than a `users.Delete(ctx)` that
reads like it might delete one thing.

### Comparison queries

```go
adults, err := users.
	Where("age", ">=", 18).
	WhereNotIn("status", []string{"blocked", "deleted"}).
	WhereNotNull("email").
	OrderBy("created_at", odm.Desc).
	Select("name", "email", "created_at").
	Limit(20).
	Get(ctx)
```

Two conditions on one field both survive — `Where("age", ">=", 18).Where("age", "<=", 65)`
means exactly that, not whichever came last.

Operators come in two spellings, and both compile to the same filter:

```go
users.Where("age", ">=", 18)        // clear at a call site
users.Where("age", odm.Gte, 18)     // a typo is a compile error
```

The typed constants are `odm.Eq`, `odm.Ne`, `odm.Gt`, `odm.Gte`, `odm.Lt` and
`odm.Lte` — the same treatment `odm.Asc`/`odm.Desc` get, for the same reason.
A misspelled string is caught too, but only when the query runs.

### OR queries

`OrWhere` ORs with **everything accumulated before it**. The chain folds
strictly left to right, with no operator precedence:

```go
users.Where(a).OrWhere(b)            // a OR b
users.Where(a).Where(b).OrWhere(c)   // (a AND b) OR c
users.Where(a).OrWhere(b).Where(c)   // (a OR b) AND c
```

Read it as "everything so far, OR this". Note the third line: that is *not*
SQL's precedence, where `AND` binds tighter and would give `a OR (b AND c)`.
Chaining can only build filters of that left-nested shape, so a grouped
predicate needs an explicit `$or`:

```go
users.Where("is_active", true).WhereRaw(bson.M{"$or": bson.A{
	bson.M{"email": email},
	bson.M{"phone": phone},
}})
```

As the first call in a chain there is nothing to OR against, so `OrWhere`
behaves like `Where`.

### Null queries

MongoDB does not have SQL's NULL, and this package doesn't pretend
otherwise:

```go
users.WhereNull("deleted_at")     // deleted_at is null OR absent
users.WhereNotNull("email")       // email is present and not null
```

For the stricter "present and explicitly null", use the native form:

```go
users.WhereRaw(bson.M{"deleted_at": bson.M{"$type": "null"}})
```

`WhereNotBetween` makes a similar choice: it compiles to an `$or` of `$lt`
and `$gt`, which requires the field to exist, rather than a `$not` around
the range, which would also match documents that have no such field.

### Projections

```go
users.Select("name", "email")            // only these (plus _id)
users.Select("name").Exclude("_id")      // only name
users.Exclude("password_hash")           // everything else
```

Fields left out decode as their zero value, so a projected model is a
partial one. MongoDB doesn't allow inclusions and exclusions in one
projection — excluding `_id` being the single exception — so an illegal
mixture is caught here rather than by the server.

### Atomic updates

Updates compile to MongoDB's own operators, not to a whole-document
rewrite:

```go
result, err := users.
	Where("_id", userID).
	Set("name", "Nana").
	Inc("login_count", 1).
	Update(ctx)
```

Repeated calls on one operator group together (`Set(...).Set(...)` → one
`$set` with both fields). A field may be targeted only once per update,
which is MongoDB's own rule — `Set("a", 1).Inc("a", 1)` is refused here
instead of failing at the server.

`Update` is `UpdateMany`; `UpdateOne` stops at one document, and `OrderBy`
decides which one — the claim-a-job pattern:

```go
job, err := jobs.
	Where("status", "pending").
	Oldest().
	Set("status", "processing").
	UpdateOne(ctx)
```

One atomic round trip, with `MatchedCount` and `ModifiedCount` straight from
the server. The sort travels with the update, which is where this package's
MongoDB 8.0 requirement comes from.

`DeleteOne` reaches the same result by a different route: the driver exposes
no sort on `deleteOne` whatever the server supports, so a sorted `DeleteOne`
goes through `findAndModify` instead. Atomic all the same, and
`DeletedCount` is exact.

### Pagination

Cursor pagination seeks straight to a page instead of counting past the ones
before it, which is what makes it stay fast deep into a collection:

```go
page, err := users.
	Where("business_id", businessID).
	OrderBy("created_at", odm.Desc).
	CursorPaginate(ctx, odm.CursorPagination{Limit: 20, Cursor: cursor})

page.Data        // []User
page.NextCursor  // hand back as Cursor for the next page
page.HasMore     // whether there is one
```

`_id` is appended to the sort as a tie-breaker unless it is already there.
That is not a detail: documents sharing a `created_at` have no order between
them, and a cursor built on `created_at` alone silently skips and reorders
rows across page boundaries. An unsorted query paginates by `_id` ascending.

The cursor is opaque — base64 of a small BSON document holding the sort it
was minted under and the last row's sort values. Handing one to a query that
sorts differently returns `odm.ErrInvalidCursor` rather than nonsense, as
does a stale or hand-edited one, so a cursor arriving from a URL needs one
error branch.

The query's own `Limit` and `Skip` are ignored — the page size is
`CursorPagination.Limit`, and seeking is what replaces skipping. Filters,
projection and the soft-delete scope all apply as usual, but a projection
has to keep every field the sort uses, since the next cursor is read out of
the documents that come back.

### Aggregation

```go
type RevenueByDay struct {
	Day   string  `bson:"_id"`
	Total float64 `bson:"total"`
}

rows, err := odm.AggregateInto[Order, RevenueByDay](ctx, orders.Where("status", "paid"), pipeline)
```

The pipeline is the driver's own `mongo.Pipeline`, passed through untouched.
`Query.Aggregate` decodes into the model; `AggregateInto` decodes into
anything else, which is the usual case for a `$group`. It is a function
rather than a method because a method cannot introduce a type parameter.

The query's filter is prepended as a `$match`, so an aggregation is scoped
the same way every other read on that query is, soft deletes included. The
cost is that this package's `$match` is the first stage the server sees, so a
pipeline that must open with `$geoNear`, `$changeStream` or `$indexStats`
needs `Raw().Aggregate`, which prepends nothing and scopes nothing.

### Bulk writes

```go
result, err := users.BulkWrite(ctx, []mongo.WriteModel{
	mongo.NewUpdateOneModel().SetFilter(...).SetUpdate(...),
	mongo.NewDeleteOneModel().SetFilter(...),
})
```

Nothing is rewritten on the way through: no soft-delete scope, no timestamp,
no hook. A bulk write is a batch of instructions you composed, and
second-guessing them is how a bulk API stops being usable for what bulk APIs
are for.

`CreateMany` is the one convenience on top — one round trip, with the same
preparation `Create` gives a single model (hooks, ULID, timestamps), every
model prepared before anything is sent:

```go
err := users.CreateMany(ctx, []*User{{Name: "Nana"}, {Name: "Kwesi"}})
```

It is MongoDB's `InsertMany`, not a transaction: by default it stops at the
first failing document and the ones before it stay written.

### Timestamps

A model embedding `odm.Model` gets `created_at`/`updated_at` handled for it:
`Create` stamps both, and `Update`/`UpdateOne` refresh `updated_at`.

```go
users.Where("_id", id).Set("name", "Nana").Update(ctx)
// $set: { name: "Nana", updated_at: <now> }
```

The stamp steps aside when you write the field yourself
(`Set("updated_at", t)`), and `WithoutTimestamps()` turns it off for one
query — a backfill, or a counter bump that shouldn't count as activity.
`UpdateRaw` never stamps anything; a raw update is entirely yours.

The clock is injectable, so a test can assert on a fixed instant rather than
on "roughly now":

```go
database := odm.New(client.Database("app"), odm.WithClock(func() time.Time {
	return fixed
}))
```

### Scopes

A scope is a named query transformation kept next to the model:

```go
func Active(q *odm.Query[User]) *odm.Query[User] {
	return q.Where("is_active", true)
}

users.Scope(Active, Verified).Where("country", "GH").Get(ctx)
```

It is an ordinary function over an immutable query, which is all it needs to
be: no context and no error return, so it cannot perform I/O, and it cannot
mutate what it was handed.

### Hooks

Implement either interface on the pointer receiver:

```go
func (u *User) BeforeCreate(ctx context.Context) error {
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	return nil
}
```

| Hook           | When                                                            |
| -------------- | --------------------------------------------------------------- |
| `BeforeCreate` | Before an insert (`Create`, `CreateMany`, or `Save` on a new model). An error aborts it. |
| `AfterCreate`  | After the insert succeeds. An error reaches the caller, but the document is already written — this cannot undo it. |
| `BeforeUpdate` | Before `Save` writes a changed model. It may change the model; the update is recomputed after it runs. |
| `AfterUpdate`  | After `Save`'s write succeeds.                                    |

`BeforeCreate` runs *before* the ULID and timestamps are assigned, so a hook
that sets its own `ID` or `CreatedAt` wins.

Hooks fire only where a model instance actually exists — `Create`,
`CreateMany` and `Save`. A query-level `Update` or `Delete` acts on every
document the filter matches without hydrating any of them, so there is no
instance to hand a hook, and manufacturing one would mean either reading
every matching document first or calling a hook on a model whose fields
don't reflect what was written. Neither is worth pretending, which is also
why there is no `BeforeDelete`: nothing in this package deletes a model
instance.

### Soft deletes

```go
type User struct {
	odm.Model       `bson:",inline"`
	odm.SoftDeletes `bson:",inline"`

	Name string `bson:"name"`
}
```

`Delete` then stamps `deleted_at` instead of removing anything, and every
read on the collection hides the stamped documents:

```go
users.Get(ctx)                 // live only
users.WithTrashed().Get(ctx)   // live and deleted
users.OnlyTrashed().Get(ctx)   // deleted only

users.Where("_id", id).Restore(ctx)      // clears deleted_at
users.Where("_id", id).ForceDelete(ctx)  // removes it for real
```

The scope is applied once, at compile time, so `WithTrashed` overrides it
wherever it appears in the chain, and it never touches a model that doesn't
embed `SoftDeletes` (where these methods return `ErrInvalidQuery`).

`DeletedAt` is a `*time.Time` with `omitempty`, so a live document carries no
`deleted_at` field at all rather than a null one. MongoDB's
`{deleted_at: null}` matches a missing field too, so both shapes read as "not
deleted" and a collection that gains soft deletes later needs no backfill.
`Restore` unsets the field rather than nulling it, leaving a restored
document shaped exactly like one that was never deleted.

Two behaviours worth knowing:

- `ForceDelete` **ignores** the default scope. Purging is the reason to reach
  for it, and silently skipping the already-trashed documents would be the
  surprising outcome. `OnlyTrashed().ForceDelete(ctx)` narrows it to a purge
  of just those.
- `Restore` only ever touches soft-deleted documents. `WithTrashed` and
  `OnlyTrashed` make no difference to it.

`Delete`'s `DeletedCount` is the underlying update's `ModifiedCount` on this
path, so soft-deleting an already-trashed document counts zero.

### Raw updates

```go
result, err := users.
	Where("_id", id).
	UpdateRaw(ctx, bson.M{
		"$set":  bson.M{"name": "Nana"},
		"$push": bson.M{"tags": bson.M{"$each": bson.A{"go", "mongo"}}},
	})
```

`update` is passed straight to the driver — a `bson.M`, a `bson.D`, or an
aggregation pipeline — so `$each`, `$rename`, positional operators and the
rest stay reachable. Nothing re-validates it. It refuses to run alongside
staged operators rather than silently dropping them.

### Deletion

```go
result, err := users.Where("status", "inactive").Delete(ctx)
```

On a model without `odm.SoftDeletes` this is a **physical** MongoDB delete —
nothing is recoverable afterwards, and an unfiltered
`users.Query().Delete(ctx)` empties the collection. With `odm.SoftDeletes` it
stamps `deleted_at` instead; see below.

### Queries are immutable

Every builder method returns a new query and leaves the receiver alone, so a
partially built query is safe to keep, branch from, and share across
goroutines:

```go
base := users.Where("is_active", true)

admins := base.Where("role", "admin")   // is_active AND admin
members := base.Where("role", "member") // is_active AND member
```

`base` still filters on `is_active` alone. Staged update operators behave the
same way:

```go
base := users.Where("_id", id)

rename := base.Set("name", "Nana")
login := base.Inc("login_count", 1)
```

`rename` sends only the `$set`, `login` only the `$inc`.

### Composing raw filters

A raw filter never replaces the conditions around it — MongoDB's own AND
semantics apply, so both must match:

```go
users.
	Where("is_active", true).
	WhereRaw(bson.M{"$or": bson.A{
		bson.M{"email": email},
		bson.M{"phone": phone},
	}}).
	Get(ctx)
```

### Errors

- `odm.ErrModelNotFound` — `First`/`Find` matched nothing. The driver's
  `mongo.ErrNoDocuments` stays wrapped underneath, so `errors.Is` finds
  either one.
- `odm.ErrDuplicateKey` — a write violated a unique index. The driver's
  `*mongo.WriteException` stays wrapped underneath, so `errors.As` still
  reaches the constraint name and the offending value.
- `odm.ErrInvalidCursor` — `CursorPaginate` was handed a cursor it can't
  use: not this encoding, from an older build, or minted under a different
  sort.
- `odm.ErrInvalidQuery` — a builder call was handed invalid input (a
  negative `Limit`, an unknown `Direction`, a non-slice `WhereIn`, a nil
  `WhereRaw`). Builder methods return `*Query[T]` and so have nowhere to put
  an error; the query records the first failure and the next terminal method
  returns it, where you're already checking an error.
- `odm.ErrNilModel` — `Create` was called with a nil model pointer.

Match these sentinels with `errors.Is`, never on the message.

### Save and dirty tracking

A model that came from the database remembers what it looked like then, so
`Save` can write just the difference:

```go
user.Name = "Nana Kwesi"
err := users.Save(ctx, &user)   // $set: { name: ..., updated_at: ... }
```

Insert or update is decided by whether the model **exists** — hydrated by a
read, or written by an earlier `Create` or `Save` — never by whether its ID
looks set. A model you built and gave an `_id` is still new.

```go
odm.Exists(&user)                    // did this come from the database?
odm.IsDirty(&user)                   // anything changed?
odm.IsDirty(&user, "email")          // that field in particular?
odm.Changes(&user)                   // (odm.Changeset, error)
odm.Original(&user, "email")         // the value before the change
odm.WasChanged(&user, "email")       // did the last Save write it?
```

`Changeset` holds `Set` (a `bson.M` of each changed field's current value)
and `Unset` (the fields that have gone away) — exactly what the update would
send.

These are functions rather than methods because they need the whole model,
and an embedded `odm.Model` can only see itself.

Four things worth knowing:

- **It is a `$set` of changes, never a whole-document replacement.** A field
  another writer changed in the meantime survives untouched unless this
  model changed it too.
- **An unchanged model is not written at all** — no round trip, no
  `updated_at`, no hooks, no observers.
- **A field the struct doesn't declare is never touched.** The snapshot is of
  the *model*, not of the document it was decoded from, so a column from an
  older schema or another service can't end up in an `$unset`.
- An `omitempty` field falling to its zero value *is* an `$unset` — that is
  the difference between "empty" and "absent", and it is deliberate.

Only models embedding `odm.Model` or `odm.IdentityModel` track state; there
is nowhere else to keep it. For anything else, `Create` and a query-level
`Update` do the same job explicitly. Models decoded by `Aggregate` are not
tracked either — a grouped row is not a document.

`IsDirty` and `WasChanged` are the same question in two tenses: what is
still unwritten, and what the last write did. `WasChanged` is what you want
after a `Save`, since by then `Changes` is empty and `Original` has moved on
to the value just written:

```go
if err := users.Save(ctx, &user); err != nil {
	return err
}
if odm.WasChanged(&user, "email") {
	// the address on file is new; send a confirmation
}
```

It describes the most recent write, so a `Save` that found nothing to do
doesn't change the answer, and an insert reports nothing changed — a new
document changed no field, it created them all.

### Observers

Observers are the same lifecycle as the model's hooks, moved outside the
model type — for behavior belonging to the application rather than to the
document:

```go
type UserObserver struct{ mailer *Mailer }

func (o UserObserver) Created(ctx context.Context, user *User) error {
	return o.mailer.Welcome(ctx, user.Email)
}

odm.Observe[User](database, UserObserver{mailer: mailer})
```

`Creating`, `Created`, `Updating` and `Updated` are each their own interface,
so an observer implements only what it needs — and one that implements none
of them panics at registration, since that is almost always a signature typo.

Registration is **per database**, not per process: there is no package-global
registry, and a test's database carries its own observers or none. Every
collection built from that database sees them, whenever it was built. The
model's own hook runs first, then observers in registration order; the first
error stops the rest and aborts the write for the `-ing` events.

### Relationships

MongoDB's first answer to "these things belong together" is to embed them.
Reach for a relation when the related documents are genuinely their own
collection — queried on their own, updated on their own, or too many to
embed.

Declare the link once, with the field it writes to kept out of the document:

```go
type Customer struct {
	odm.Model `bson:",inline"`

	Name   string  `bson:"name"`
	Orders []Order `bson:"-"`     // loaded, never stored
}

var CustomerOrders = odm.HasMany[Customer, Order]{
	ForeignKey: "customer_id",
	Attach:     func(c *Customer, orders []Order) { c.Orders = orders },
}

customers, err := customers.With(CustomerOrders).Get(ctx)
```

| Declaration                 | Where the key lives | Attach receives |
| --------------------------- | ------------------- | --------------- |
| `HasMany[T, R]`             | on the related model | `[]R`, empty when there are none |
| `HasOne[T, R]`              | on the related model | `*R`, nil when there is none     |
| `BelongsTo[T, R]`           | on this model        | `*R`, nil when the key is unset or dangling |
| `BelongsToMany[T, R]`       | on this model, as a list | `[]R`, empty when there are none |

`ForeignKey` names the field holding the key; `LocalKey` (or `OwnerKey` on
`BelongsTo`) names what it points at, defaulting to `_id`.

`Attach` is a function rather than a field name this package would find by
reflection: the compiler checks it, and there is no string to get wrong. It
is also the only part of a relation that isn't reflection-free — the keys are
read straight from the documents' BSON bytes, by the field names you
declared.

**Loading batches, always.** Each relation costs exactly one extra query
whatever the number of parents: the keys are collected and the related
documents fetched with a single `$in`. Two relations over ten parents is
three queries, not twenty-one. The integration suite asserts this with a
command monitor rather than taking it on trust.

A relation can carry its own, and the levels stay batched:

```go
customers.With(CustomerOrders.With(OrderPayments)).Get(ctx)
```

Three queries for three levels — customers, then every order belonging to
any of them, then every payment belonging to any of those — however many
rows there are at each. `With` copies the declaration rather than changing
it, so one exported relation works both plain and nested.

`With` applies to `Get`, `First`, `Find` and `CursorPaginate`. Nothing loads
on field access — reading `customer.Orders` is reading a struct field, never
a query.

#### Many-to-many

MongoDB's answer to a many-to-many is a list of ids on one side, because a
document can hold one where a relational row cannot. There is no join
collection in it — the list *is* the relationship:

```go
type User struct {
	odm.Model `bson:",inline"`

	RoleIDs []string `bson:"role_ids"`
	Roles   []Role   `bson:"-"`
}

var UserRoles = odm.BelongsToMany[User, Role]{
	LocalKey: "role_ids",
	Attach:   func(u *User, roles []Role) { u.Roles = roles },
}
```

The other direction needs no new declaration. Where the list lives on the
*related* model, that is a `HasMany` whose foreign key happens to hold an
array, and it reads it the same way:

```go
var RoleUsers = odm.HasMany[Role, User]{
	ForeignKey: "role_ids",
	Attach:     func(r *Role, users []User) { r.Users = users },
}
```

That symmetry is not a special case bolted on: a relation key is read as a
list of keys, one for a scalar field and one per element for an array, so
every declaration handles both without knowing which it has. Loading stays
one query — every id from every parent goes into a single `$in` — and a
parent listing the same id twice gets the document once.

A join collection carrying its own fields (when a role was granted, and by
whom) is a different shape. That is a model of its own with two `BelongsTo`
relations, which needs nothing from this package.

Not here: polymorphic relations. They have no single related type, so
`Relation[T]` could not name one, `Attach` would take `[]any`, and every
call site would need a type switch — the one untyped corner in a package
whose relations are otherwise compile-checked. Two typed relations and a
branch in your own code reads better.

### Transactions

```go
err := database.Transaction(ctx, func(ctx context.Context) error {
	if err := users.Create(ctx, &user); err != nil {
		return err
	}
	return accounts.Create(ctx, &account)
})
```

Returning nil commits, returning an error aborts.

**The context handed to the callback *is* the transaction.** It carries the
session, which is how the driver knows an operation belongs to the
transaction, so every call inside must use that one — a call using the outer
`ctx` runs outside the transaction and commits on its own. That is also why
there is no transactional collection type to construct: the collections you
already have take part by being given this context. Reach the session
directly with `mongo.SessionFromContext(ctx)`.

Two things to know, both MongoDB's rather than this package's:

- Transactions need a replica set or sharded cluster. On a standalone server
  the driver errors rather than running the callback unprotected.
- **The callback can run more than once.** This uses the driver's
  `WithTransaction`, which is MongoDB's own retry recommendation: it retries
  on a transient transaction error and retries a commit whose outcome is
  unknown, giving up after about two minutes. Keep the callback idempotent,
  and keep anything that isn't a database write — sending mail, charging a
  card — outside it.

### Indexes

```go
func (User) Indexes() []odm.Index {
	return []odm.Index{
		{Keys: bson.D{{Key: "email", Value: 1}}, Unique: true},
		{Keys: bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}}},
	}
}

err := users.SyncIndexes(ctx)
```

`Index` carries `Keys`, `Name`, `Unique`, `Sparse`, `ExpireAfter` (a TTL
index) and `PartialFilter`, and compiles to the driver's `mongo.IndexModel`.
Anything beyond that — collation, wildcard, text or geo options — belongs on
`Raw().Indexes()`, which this never gets in the way of.

`SyncIndexes` is safe to call on every start: creating an index that already
exists with the same specification does nothing. It **only ever adds**. An
index no longer declared is left alone, and one whose declaration changed
reports MongoDB's own conflict error rather than being quietly rebuilt —
dropping an index should be a deliberate act against `Raw().Indexes()`, not
a side effect of a deploy.

A unique index is what makes `ErrDuplicateKey` reachable:

```go
if errors.Is(err, odm.ErrDuplicateKey) {
	// that email is taken
}
```

### Escape hatches

```go
database.Raw()  // *mongo.Database — RunCommand, GridFS, change streams
users.Raw()     // *mongo.Collection — index management, FindOneAndUpdate, InsertOneResult
users.WhereRaw(bson.M{...})
users.Where(...).UpdateRaw(ctx, bson.M{...})
users.Raw().Aggregate(ctx, pipeline)  // no $match prepended, no scope applied
```

## Scope

Deliberately not here yet: many-to-many and polymorphic relations, a fluent
aggregation builder (the pipeline is already native), model factories,
migrations. `Raw()` covers all of them in the meantime.

There is no `FirstOrFail`/`FindOrFail`: `First` and `Find` already return
`odm.ErrModelNotFound` rather than a zero value you have to check, so the
pair would be the same method twice.

## Tests

```
go test ./odm/...                    # query building, collection names — no Mongo needed
go test -tags=integration ./odm/...  # real MongoDB, via testcontainers (needs Docker)
go test -bench . ./odm/...           # compilation costs, for spotting regressions
```

`make pull-images` fetches every container the integration suite starts, all
at once — worth running first on a cold machine, since otherwise each
package pulls its own as it runs.

The unit suite covers everything that can be decided without a server: what
BSON a query compiles to, what an update stages, what changed on a model,
how a cursor encodes. The integration suite covers everything where MongoDB's
own behavior is the answer — null versus missing, `$nin` on an empty list,
projection rules, array updates, index conflicts, transaction rollback — and
starts one container to do it: MongoDB 8 as a replica set, because
transactions need one and it is closer to what anything using this package
runs against anyway.

### Checking your own models

`odm.TestConformance` holds a model you wrote to what this package expects:

```go
func TestUserModel(t *testing.T) {
	odm.TestConformance(t, odm.Use[User](database), func() *User {
		return &User{Name: "Nana", Email: "nana@example.com"}
	})
}
```

[`cache`](../cache) and [`storage`](../storage) export a suite of the same
name for the opposite reason — they hold several backends to one interface.
There is only one ODM here, so the useful question isn't whether it behaves
but whether a model wires into it correctly. The mistakes it catches are the
quiet ones: an embedded `odm.Model` without `bson:",inline"` (which nests
`_id` under a `model` key and still compiles), two fields tagged to the same
name, a relation field that isn't `bson:"-"` and so gets persisted.

It adapts to what the model embeds — soft deletes, timestamps and tracked
state are only checked where they exist, and `SyncIndexes` only where the
model implements `odm.Indexer`. Give it a collection of its own: it writes,
reads and deletes.

## Examples

Compile-checked snippets live in the package documentation:

```
go doc github.com/nanaaikinson/chandlery/odm
```

[`examples/odm`](../examples/odm) is the same material as one runnable
program against a real MongoDB.

## Stability

Pre-v1: the API may still shift. Nothing here is marked experimental — the
whole package is, in the sense that names can change before v1.

Three things flagged in an earlier review have been settled:

- **`Where`'s operator is typed now**, as `odm.Gt` and friends, with the
  string spelling still accepted. Keeping both is the price of a `Where` that
  takes two arguments or three; typing one of them is what makes a typo a
  compile error.
- **`Changes` returns a `Changeset`**, not a three-value tuple.
- **`Observe` still takes `...any`**, and will. Go cannot express "implements
  at least one of these four interfaces": a single `Observer[T]` would force
  every observer to implement all four events, and one registration function
  per event would turn an observer watching three of them into three calls.
  The mismatch it can't catch at compile time, it panics on at registration.

The parts least likely to move are the ones the rest is built on: query
immutability, `Raw()` at every layer, context on every operation, and the
sentinel errors.
