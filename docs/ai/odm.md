# Chandlery ODM — Agent Instructions

## Purpose

Chandlery is a collection of small, independent, reusable Go packages.

The MongoDB ODM belongs in:

```text
github.com/nanaaikinson/chandlery/odm
```

It is a top-level package and a sibling of `db`.

```text
chandlery/
├── cache/
├── db/
├── odm/
├── respond/
├── storage/
└── validator/
```

`db` and `odm` represent different persistence paradigms:

- `db` is relational and Bun-backed.
- `odm` is document-oriented and backed by the official MongoDB Go driver.

They MUST NOT import each other.

---

# Core Goal

Build an idiomatic Go ODM on top of:

```text
go.mongodb.org/mongo-driver/v2
```

The developer experience should be inspired by Laravel Eloquent while preserving MongoDB semantics.

The goal is NOT to port Eloquent literally.

The goal is to make MongoDB access feel:

- expressive,
- predictable,
- composable,
- type-aware,
- easy to test,
- easy to escape into native MongoDB when needed.

Example target usage:

```go
users := odm.Use[User](database)

user, err := users.
    Where("email", email).
    Where("is_active", true).
    First(ctx)
```

Another example:

```go
users, err := odm.Use[User](database).
    WhereIn("status", []string{"active", "pending"}).
    OrderBy("created_at", odm.Desc).
    Limit(20).
    Get(ctx)
```

MongoDB-native operations must remain available:

```go
users, err := odm.Use[User](database).
    WhereRaw(bson.M{
        "metadata.source": "api",
    }).
    Get(ctx)
```

---

# Architectural Principles

## 1. Use the official MongoDB driver

The ODM wraps the official MongoDB Go driver.

Do not implement:

- a custom transport,
- a custom BSON encoder,
- a MongoDB protocol implementation,
- a custom connection pool.

Use driver primitives whenever appropriate:

```go
*mongo.Client
*mongo.Database
*mongo.Collection
mongo.Pipeline
bson.M
bson.D
options.FindOptionsBuilder
```

---

## 2. Do not hide MongoDB

Every major abstraction should have a native escape hatch.

Examples:

```go
func (db *DB) Raw() *mongo.Database
```

```go
func (c *Collection[T]) Raw() *mongo.Collection
```

Queries should support:

```go
WhereRaw(...)
```

Aggregation APIs should accept native:

```go
mongo.Pipeline
```

Do not create an abstraction that prevents use of MongoDB features.

---

## 3. No global database state

Do NOT implement:

```go
odm.SetDefaultDatabase(...)
odm.Connect(...)
odm.Default(...)
```

Application code owns the MongoDB client and database.

Preferred:

```go
client, err := mongo.Connect(
    options.Client().ApplyURI(uri),
)

database := odm.New(client.Database("app"))
```

Then:

```go
users := odm.Use[User](database)
```

---

## 4. Context is mandatory for I/O

Every operation that performs database I/O must receive:

```go
context.Context
```

Example:

```go
First(ctx)
Get(ctx)
Create(ctx, &model)
Update(ctx, values)
Delete(ctx)
Count(ctx)
Exists(ctx)
```

Do not store contexts in structs.

---

## 5. Query builders must not leak state

A query must be safe to derive from another query.

This must work:

```go
base := users.Where("status", "active")

admins := base.Where("role", "admin")
customers := base.Where("role", "customer")
```

`admins` must not modify `base`.

`customers` must not contain the admin filter.

Use copy-on-write or an equivalent immutable-builder approach.

---

# Package Boundaries

Initial package:

```text
odm/
├── odm.go
├── model.go
├── collection.go
├── query.go
├── filter.go
├── update.go
├── errors.go
└── *_test.go
```

Do NOT prematurely create a large directory hierarchy.

Introduce packages or files only when they represent a real architectural boundary.

Potential later structure:

```text
odm/
├── hooks.go
├── scopes.go
├── soft_delete.go
├── pagination.go
├── transaction.go
├── aggregation.go
├── relation.go
├── has_one.go
├── has_many.go
├── belongs_to.go
└── internal/
    ├── metadata.go
    └── fields.go
```

---

# Database Type

The ODM may wrap `*mongo.Database`:

```go
type DB struct {
    database *mongo.Database
}
```

Constructor:

```go
func New(database *mongo.Database) *DB
```

Raw access:

```go
func (db *DB) Raw() *mongo.Database
```

Do not connect to MongoDB inside `odm.New`.

---

# Collections

Use a generic collection abstraction:

```go
type Collection[T any] struct {
    db         *DB
    collection *mongo.Collection
}
```

Preferred entry point:

```go
users := odm.Use[User](db)
```

`Use[T]` should resolve the collection name and return a reusable `Collection[T]`.

A collection must be safe for concurrent use.

---

# Models

Initial base model should be small.

For Chandlery, ULID support is preferred unless a feature specifically requires MongoDB ObjectIDs.

Example:

```go
type Model struct {
    ID        ulid.ULID `bson:"_id" json:"id"`
    CreatedAt time.Time `bson:"created_at" json:"created_at"`
    UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}
```

Models may embed it:

```go
type User struct {
    odm.Model `bson:",inline"`

    Name     string `bson:"name"`
    Email    string `bson:"email"`
    IsActive bool   `bson:"is_active"`
}
```

Do not assume every model must embed `odm.Model`.

The ODM should eventually support custom ID types.

---

# Collection Names

Collection names should support explicit declaration.

Example interface:

```go
type CollectionNamer interface {
    CollectionName() string
}
```

Example:

```go
func (User) CollectionName() string {
    return "users"
}
```

A reasonable automatic fallback may be implemented later.

Do not build complicated pluralization rules prematurely.

---

# Query API

The target API should feel approximately like:

```go
users.
    Where("status", "active").
    Where("age", ">", 18).
    WhereIn("country", []string{"GH", "NG"}).
    OrderBy("created_at", odm.Desc).
    Limit(20).
    Get(ctx)
```

Initial operations:

```text
Where
OrWhere
WhereIn
WhereNotIn
WhereNull
WhereNotNull

OrderBy
Limit
Skip

First
Find
Get
Count
Exists

Create
Update
Delete
```

Do not implement every planned query operator in the first PR.

---

# Where Semantics

Support convenient forms such as:

```go
Where("email", email)
```

Equivalent to:

```text
email == value
```

Later:

```go
Where("age", ">", 18)
```

may map to:

```go
bson.M{
    "age": bson.M{
        "$gt": 18,
    },
}
```

Supported operators should be explicit and validated.

Possible mapping:

```text
=   -> direct equality
!=  -> $ne
>   -> $gt
>=  -> $gte
<   -> $lt
<=  -> $lte
```

Invalid operators must return an error rather than silently generating incorrect BSON.

---

# Native Filters

Always retain a native filter path.

Example:

```go
WhereRaw(bson.M{
    "$or": bson.A{
        bson.M{"email": email},
        bson.M{"phone": phone},
    },
})
```

Do not attempt to wrap every MongoDB query operator.

---

# Updates

MongoDB update operators must be first-class.

Eventually support patterns such as:

```go
users.
    Where("_id", id).
    Set("name", "Nana").
    Inc("login_count", 1).
    Update(ctx)
```

Or a typed update description:

```go
odm.Update{
    Set: bson.M{
        "name": "Nana",
    },
    Inc: bson.M{
        "login_count": 1,
    },
}
```

Native Mongo update documents must remain usable.

Do not reduce MongoDB updates to SQL-style field assignment only.

---

# Error Handling

Do not expose driver errors as the only public contract.

Define appropriate sentinel or typed errors when the ODM has meaningful semantics.

Examples:

```go
var ErrModelNotFound = errors.New("odm: model not found")
```

Potential later errors:

```text
ErrModelNotFound
ErrInvalidOperator
ErrInvalidModel
ErrDuplicateKey
```

Preserve wrapped driver errors where useful.

Callers must be able to use:

```go
errors.Is(...)
errors.As(...)
```

Do not depend on error-string matching.

---

# Timestamps

Automatic timestamps should eventually support:

```go
created_at
updated_at
```

The behavior must be deterministic and testable.

Do not hard-code `time.Now()` deeply into logic that needs deterministic tests.

Prefer an injectable clock/function if timestamp logic becomes substantial.

---

# Soft Deletes

Soft deletion belongs in the ODM but is NOT part of the first minimal implementation unless explicitly requested.

Desired future behavior:

```go
users.Delete(ctx)
```

sets:

```text
deleted_at
```

while:

```go
WithTrashed()
OnlyTrashed()
Restore()
ForceDelete()
```

provide Eloquent-like semantics.

Implement this as query behavior/global scope semantics, not duplicated filters throughout the codebase.

---

# Scopes

Future local scope pattern:

```go
func Active(q odm.Query[User]) odm.Query[User] {
    return q.Where("is_active", true)
}
```

Usage:

```go
users.
    Scope(Active).
    Get(ctx)
```

Scopes must be composable.

---

# Hooks

Prefer interfaces over reflection.

Example:

```go
type BeforeCreate interface {
    BeforeCreate(context.Context) error
}
```

A model may implement:

```go
func (u *User) BeforeCreate(ctx context.Context) error {
    u.Email = strings.ToLower(u.Email)
    return nil
}
```

Do not use reflection to discover methods that interfaces can express directly.

---

# Relationships

Relationships are a future phase.

Possible relation names:

```text
BelongsTo
HasOne
HasMany
```

Do not pretend MongoDB is relational.

Support MongoDB-native embedding as a first-class modeling strategy.

Where relationships reference other collections, eager loading must batch related queries.

Never intentionally implement N+1 behavior such as:

```text
1 users query
100 order queries
```

Prefer:

```text
1 users query
1 orders query using $in
```

---

# Aggregations

MongoDB aggregation must remain first-class.

Do not build an abstraction that hides `mongo.Pipeline`.

At minimum future APIs should accept:

```go
mongo.Pipeline
```

A fluent aggregation builder may come later.

---

# Pagination

Cursor pagination should be preferred for large MongoDB collections.

Desired result:

```go
type CursorPage[T any] struct {
    Data       []T   `json:"data"`
    NextCursor string `json:"next_cursor,omitempty"`
    HasMore    bool   `json:"has_more"`
}
```

Offset pagination may also exist but is not the preferred strategy for large datasets.

---

# Reflection Rules

Reflection may be used for:

- model type metadata,
- BSON tag inspection,
- collection-name discovery,
- optional embedded model state,
- relation metadata if introduced later.

Reflection must NOT be used as the default mechanism for everything.

Cache reflection-derived metadata.

Never repeatedly inspect the same model type on every query.

---

# Performance Rules

Avoid:

- repeated model reflection,
- unnecessary BSON marshal/unmarshal cycles,
- N+1 relation loading,
- copying large documents without need,
- building enormous intermediate maps for simple filters.

Prefer simple driver-compatible values.

---

# Testing

All added behavior requires tests.

Pure query-building logic should use unit tests.

Examples:

```text
Where equality
comparison operators
WhereIn
WhereRaw composition
query immutability
sort handling
limit/skip
invalid operators
collection-name resolution
```

Anything requiring actual MongoDB behavior should use a real MongoDB testcontainer behind:

```go
//go:build integration
```

Test real behavior rather than mocking MongoDB semantics.

Examples:

```text
insert document
find document
update document
delete document
unique index violation
transaction behavior
aggregation behavior
```

`go test ./...` must remain dependency-free and fast.

Integration tests may run separately.

---

# Implementation Discipline

Before implementing a feature:

1. Inspect existing Chandlery conventions.
2. Identify the smallest public API needed.
3. Write tests describing the behavior.
4. Implement the feature.
5. Run formatting and tests.
6. Confirm no new unwanted package coupling was introduced.
7. Document public APIs where appropriate.

Do not redesign unrelated packages.

Do not modify `db` to accommodate MongoDB.

Do not add abstractions merely because Laravel has them.

---

# Non-goals

Do NOT initially implement:

```text
full Laravel Eloquent compatibility
SQL-style joins
magic property lazy loading
dynamic method names
Active Record global state
schema migration DSL
model factories
polymorphic relationships
pivot models
automatic relation discovery
model serialization framework
CLI generators
plugin system
```

These require separate justification.

---

# Design Test

For every proposed abstraction ask:

> Does this improve the Go developer experience while still allowing MongoDB to behave like MongoDB?

If the abstraction only exists to make MongoDB pretend to be SQL/Eloquent, do not add it.

---

# Example Target

```go
type User struct {
    odm.Model `bson:",inline"`

    Name     string `bson:"name"`
    Email    string `bson:"email"`
    IsActive bool   `bson:"is_active"`
}

func GetActiveUser(
    ctx context.Context,
    database *odm.DB,
    email string,
) (*User, error) {
    users := odm.Use[User](database)

    return users.
        Where("email", email).
        Where("is_active", true).
        First(ctx)
}
```

The API should be unsurprising to a Go developer and familiar to someone who has used Eloquent.
