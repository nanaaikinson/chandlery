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
`Create` fills in. A struct with its own `bson:"_id"` — or none at all,
letting Mongo generate an ObjectID — works everywhere else in the package.

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
| `Where(field, operator, value)`    | `=`, `==`, `!=`, `>`, `>=`, `<`, `<=`. Anything else is an error.                                               |
| `OrWhere(field, [operator,] value)` | ORs with everything accumulated so far — see below.                                                           |
| `WhereIn(field, values)`          | `$in`. `values` must be a slice or array.                                                                       |
| `WhereNotIn(field, values)`       | `$nin`. Note an empty slice matches *everything*.                                                               |
| `WhereNull(field)`                | `{field: null}` — matches null **and missing**.                                                                 |
| `WhereNotNull(field)`             | `{field: {$ne: null}}` — present and non-null.                                                                  |
| `WhereBetween(field, from, to)`   | Inclusive `$gte`/`$lte`.                                                                                        |
| `WhereNotBetween(field, from, to)` | `$or` of `$lt`/`$gt`; needs the field to exist.                                                                |
| `WhereRaw(filter)`                | A driver-native BSON document (`bson.M`, `bson.D`, `bson.Raw`, or a tagged struct), ANDed with everything else. |
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
| `Create(ctx, &model)`             | On the collection, not the query.                                                                               |

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
| `Delete(ctx)`              | `DeleteMany` → `*mongo.DeleteResult`. **Physical delete.**          |
| `DeleteOne(ctx)`           | Same, against one document — `OrderBy` picks which.                 |

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

That is atomic on every supported server, but it doesn't reach the server
the same way on all of them. MongoDB 8.0 takes a sort on `updateOne`
directly; earlier versions reject the field, so a sorted `UpdateOne` goes
through `findAndModify` there instead. Which one to use is settled by a
`hello` probe made once per `odm.DB`, on the first sorted `UpdateOne` and
never again — an unsorted `UpdateOne` is a plain `updateOne` everywhere and
probes nothing.

One wrinkle on the older path: `findAndModify` reports whether a document
matched, not whether the write changed it, so `ModifiedCount` mirrors
`MatchedCount` there. On MongoDB 8.0 — and on any unsorted `UpdateOne` —
both counts are the server's own. `DeleteOne` has no such caveat:
`deleteOne` takes no sort on any version, so a sorted one always uses
`findAndModify`, and `DeletedCount` is exact either way.

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

This is a **physical** MongoDB delete. There are no soft deletes in this
package, so nothing is recoverable afterwards, and an unfiltered
`users.Query().Delete(ctx)` empties the collection.

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
- `odm.ErrInvalidQuery` — a builder call was handed invalid input (a
  negative `Limit`, an unknown `Direction`, a non-slice `WhereIn`, a nil
  `WhereRaw`). Builder methods return `*Query[T]` and so have nowhere to put
  an error; the query records the first failure and the next terminal method
  returns it, where you're already checking an error.
- `odm.ErrNilModel` — `Create` was called with a nil model pointer.

Match these sentinels with `errors.Is`, never on the message.

### Escape hatches

```go
database.Raw()  // *mongo.Database — aggregations, indexes, transactions, RunCommand
users.Raw()     // *mongo.Collection — bulk writes, UpdateMany, InsertOneResult
users.WhereRaw(bson.M{...})
```

## Scope

Deliberately not here yet: soft deletes, timestamps beyond what `Create`
stamps, hooks and observers, scopes, dirty tracking, `Save`, relationships
and eager loading, cursor pagination, an aggregation builder, a transaction
abstraction, index declarations, model factories, migrations. `Raw()` covers
all of them in the meantime.

There is no `FirstOrFail`/`FindOrFail`: `First` and `Find` already return
`odm.ErrModelNotFound` rather than a zero value you have to check, so the
pair would be the same method twice.

## Tests

```
go test ./odm/...                    # query building, collection names — no Mongo needed
go test -tags=integration ./odm/...  # real MongoDB, via testcontainers (needs Docker)
```

The integration suite starts two containers, MongoDB 8 and MongoDB 7, and
runs the version-sensitive tests against both — the only way to know that
each branch of the sorted `UpdateOne` actually works on the server it
targets.
