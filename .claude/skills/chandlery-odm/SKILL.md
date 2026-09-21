---
name: chandlery-odm
description: >
  Use when designing, implementing, testing, reviewing, or extending the
  Chandlery MongoDB ODM package. Applies to query builders, models,
  collections, MongoDB filters and updates, scopes, hooks, soft deletes,
  pagination, aggregation, transactions, indexes, and relationships.
---

# Chandlery ODM Skill

Use this skill whenever work involves:

```text
odm/
MongoDB persistence
Collection[T]
Query[T]
Mongo filters
Mongo update operators
Mongo aggregations
Mongo relationships
Mongo pagination
Mongo model lifecycle behavior
```

# Mental Model

Chandlery's ODM is:

> An idiomatic Go document mapper built on the official MongoDB Go driver,
> with an Eloquent-inspired developer experience.

It is NOT:

> Laravel Eloquent translated line-by-line into Go.

Always preserve MongoDB's document model.

---

# Decision Priority

When choosing between competing designs, prefer in this order:

1. Correct MongoDB semantics.
2. Idiomatic Go.
3. Predictable public APIs.
4. Native-driver interoperability.
5. Eloquent familiarity.
6. Convenience.

Never choose Laravel familiarity over correct MongoDB behavior.

---

# API Design Heuristic

Good:

```go
users.
    Where("active", true).
    WhereIn("country", []string{"GH", "NG"}).
    OrderBy("created_at", odm.Desc).
    Get(ctx)
```

Good:

```go
users.
    WhereRaw(bson.M{
        "$text": bson.M{
            "$search": search,
        },
    }).
    Get(ctx)
```

Bad:

```go
User.Find(...)
```

if it depends on hidden package-global state.

Bad:

```go
User.Orders
```

if accessing a field secretly performs network I/O.

Go should remain explicit about I/O.

---

# Query Builder Rules

Query builders must behave as values.

Given:

```go
base := users.Where("status", "active")

admins := base.Where("role", "admin")
members := base.Where("role", "member")
```

the resulting logical filters should be:

```text
base:
status = active

admins:
status = active
AND role = admin

members:
status = active
AND role = member
```

Never mutate `base` through either derived query.

---

# Filter Composition

Prefer predictable `$and` semantics when independently constructed filters need combining.

Conceptual example:

```go
Where("active", true)

WhereRaw(
    bson.M{
        "$or": bson.A{
            bson.M{"email": email},
            bson.M{"phone": phone},
        },
    },
)
```

should represent:

```text
active == true
AND
(email == X OR phone == Y)
```

Do not merge arbitrary maps in a way that can overwrite duplicate keys.

---

# Operator Mapping

When implementing comparison operators:

```text
=   equality
!=  $ne
>   $gt
>=  $gte
<   $lt
<=  $lte
```

Example:

```go
Where("age", ">", 18)
```

becomes conceptually:

```go
bson.M{
    "age": bson.M{
        "$gt": 18,
    },
}
```

Validate unsupported operators.

Never silently reinterpret an unknown operator.

---

# MongoDB-native Features

Do not force MongoDB behavior through SQL-shaped abstractions.

First-class concepts include:

```text
embedded documents
nested fields
arrays
$set
$unset
$inc
$push
$pull
$addToSet
$elemMatch
aggregation pipelines
indexes
transactions
change streams, if later needed
```

Example nested query:

```go
Where("profile.address.country", "GH")
```

This should remain natural.

---

# Updates

Preferred future style:

```go
users.
    Where("_id", userID).
    Set("name", "Nana").
    Inc("login_count", 1).
    Update(ctx)
```

or:

```go
users.Update(ctx, odm.Update{
    Set: bson.M{
        "name": "Nana",
    },
    Inc: bson.M{
        "login_count": 1,
    },
})
```

Internally compile updates into native MongoDB update documents.

Do not emulate SQL UPDATE if that prevents using atomic Mongo operators.

---

# Models

A Chandlery Mongo model may look like:

```go
type User struct {
    odm.Model `bson:",inline"`

    BusinessID ulid.ULID `bson:"business_id"`
    Name       string    `bson:"name"`
    Email      string    `bson:"email"`
}
```

Models do not need to embed `odm.Model`.

Avoid designs where ODM functionality requires a complex base-class analogue.

Go uses composition, interfaces, and generic helpers.

---

# Hooks

When lifecycle hooks are added, prefer interfaces.

Good:

```go
type BeforeCreate interface {
    BeforeCreate(context.Context) error
}
```

Example:

```go
func (u *User) BeforeCreate(ctx context.Context) error {
    u.Email = strings.ToLower(strings.TrimSpace(u.Email))
    return nil
}
```

Do not use reflection when normal interface assertions suffice.

---

# Scopes

Scopes are query transformations.

Example:

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

A scope must not execute I/O.

A scope returns another query.

---

# Soft Deletes

Soft deletion should eventually add a default query constraint:

```text
deleted_at does not represent a deleted document
```

Normal:

```go
users.Get(ctx)
```

should exclude soft-deleted models.

Explicit:

```go
users.WithTrashed()
users.OnlyTrashed()
```

must alter that scope.

Force deletion should call actual MongoDB deletion.

Restoring should clear `deleted_at`.

Keep these semantics centralized.

---

# Relationships

Before adding a relationship, ask:

> Should this data actually be embedded?

MongoDB often favors embedding where SQL would create another table.

For cross-collection relations:

```text
BelongsTo
HasOne
HasMany
```

may exist.

Example:

```go
type Order struct {
    odm.Model

    CustomerID ulid.ULID `bson:"customer_id"`
}
```

Eager loading must batch.

Never design:

```text
fetch 100 users
run 100 order queries
```

Prefer:

```text
fetch users

collect IDs

orders filter:
user_id IN [ids...]

group orders in memory
```

---

# Aggregation

Raw aggregation must always work:

```go
results, err := collection.Aggregate(
    ctx,
    mongo.Pipeline{
        bson.D{
            {"$match", bson.M{
                "status": "completed",
            }},
        },
    },
)
```

A fluent aggregation builder must be optional convenience.

Do not wrap the MongoDB aggregation language so deeply that users cannot express native stages.

---

# Pagination

Prefer cursor pagination for high-volume collections.

An `_id` or another indexed stable field may be used as the cursor basis.

Ordering and cursor conditions must agree.

Example descending pagination:

```text
sort:
created_at DESC
_id DESC
```

next page filter conceptually:

```text
created_at < lastCreatedAt

OR

created_at == lastCreatedAt
AND _id < lastID
```

Never implement unstable cursor pagination that can duplicate or skip records under equal sort keys.

---

# Indexes

When index declarations are introduced, they should compile to driver-native models.

Example model API:

```go
func (User) Indexes() []odm.Index {
    return []odm.Index{
        {
            Keys: bson.D{
                {"email", 1},
            },
            Unique: true,
        },
    }
}
```

Internally this should eventually become:

```go
mongo.IndexModel
```

Do not invent incompatible index semantics.

---

# Errors

Normalize errors only when the ODM adds meaningful semantics.

Examples:

```text
model not found
duplicate key
invalid query operator
invalid pagination cursor
```

Retain wrapped driver information.

Avoid hiding diagnostics behind generic errors.

---

# Reflection Checklist

Before using reflection, ask:

1. Can generics solve this?
2. Can an interface solve this?
3. Can BSON tags already solve this?
4. Can the official BSON codec registry solve this?

Use reflection only if those are insufficient.

Cache any reflection-derived metadata.

---

# Testing Strategy

## Unit Test

Use unit tests for:

```text
query state
filter compilation
update compilation
scope application
cursor encoding
metadata resolution
query immutability
```

These tests should not need MongoDB.

## Integration Test

Use real MongoDB for:

```text
CRUD
indexes
transactions
aggregation
unique-key errors
relationship loading
actual update operators
```

Prefer testcontainers.

Do not build a fake MongoDB query engine.

---

# Feature Implementation Template

For every new ODM feature:

## Step 1 — Define desired calling code

Example:

```go
users.
    Where("status", "active").
    Latest().
    Limit(10).
    Get(ctx)
```

## Step 2 — Define MongoDB semantics

Example:

```text
filter:
status = active

sort:
created_at DESC

limit:
10
```

## Step 3 — Identify public types

Prefer the smallest possible API.

## Step 4 — Write unit tests

Include composition and immutability.

## Step 5 — Implement compilation

Keep database execution separate from query construction where practical.

## Step 6 — Add integration tests

Only where actual MongoDB behavior matters.

## Step 7 — Preserve raw access

Verify users can still express unsupported native MongoDB behavior.

---

# Review Checklist

When reviewing ODM code, check:

- Does it preserve MongoDB behavior?
- Does it introduce SQL assumptions?
- Is the query builder immutable?
- Is context required for I/O?
- Is there hidden global state?
- Is reflection necessary?
- Is reflection cached?
- Are BSON tags respected?
- Is raw MongoDB still accessible?
- Could this create N+1 queries?
- Could concurrent use race?
- Are errors compatible with `errors.Is` / `errors.As`?
- Does every new behavior have tests?
- Could the API be smaller?
- Is this feature actually needed in this phase?

---

# Avoid

Avoid APIs that resemble:

```go
odm.DefaultDB
odm.ConnectGlobally(...)
model.LoadRelationsAutomatically()
model.SaveWithoutContext()
```

Avoid magical I/O.

Avoid coupling:

```text
odm -> db
db -> odm
```

Avoid turning every MongoDB document into a pseudo-relational entity.

---

# Principle

The final design should make someone say:

> This feels familiar if I know Eloquent, but it still feels like Go and MongoDB.

That is the target.

# Phase 2 Skill Addendum — Queries, Updates, and Deletion

Append this section to:

```text
.claude/skills/chandlery-odm/SKILL.md
```

Use these rules whenever modifying Phase 2 querying, projections, updates, or deletion.

---

# Query Compilation Principle

Treat fluent query methods as declarations.

They should not mutate BSON directly in ways that make future composition unreliable.

Conceptually:

```text
Where(...)
WhereIn(...)
WhereRaw(...)
OrWhere(...)
```

build logical query state.

Execution later compiles that state to MongoDB BSON.

This distinction is important because expressions such as:

```go
Where("age", ">=", 18).
Where("age", "<=", 65)
```

must retain both conditions.

Naively doing:

```go
filter["age"] = ...
```

twice is incorrect.

---

# Preserve Duplicate Constraints

Always consider duplicate field predicates.

Example:

```go
Where("created_at", ">=", start).
Where("created_at", "<=", end)
```

Correct conceptual result:

```text
created_at >= start
AND created_at <= end
```

Never overwrite one predicate with another.

Using `$and` is acceptable even if a more compact MongoDB representation exists.

Correctness and predictable composition are more important than minimizing BSON size.

---

# Raw Filter Composition

Raw filters are opaque user expressions.

Do not attempt to flatten or reinterpret arbitrary raw BSON.

Given:

```go
Where("tenant_id", tenantID).
WhereRaw(raw)
```

the safest semantic model is:

```text
tenant_id condition
AND
raw expression
```

Preserve the raw expression intact.

---

# OR Semantics

`OrWhere` is easy to make ambiguous.

Before changing OR behavior, write the desired logical expression explicitly.

For example:

```go
Where("active", true).
OrWhere("role", "admin")
```

must have a documented equivalent such as:

```text
active == true OR role == admin
```

Do not let internal slice ordering accidentally define undocumented grouping behavior.

Nested predicate groups are not required in Phase 2 unless explicitly requested.

If grouping becomes necessary later, introduce an explicit grouped-query API rather than relying on hidden precedence rules.

---

# Null Semantics

MongoDB null behavior is not SQL NULL behavior.

Before implementing or changing:

```go
WhereNull(field)
WhereNotNull(field)
```

verify what MongoDB matches for:

```text
field missing
field present with null value
field present with non-null value
```

Document the actual MongoDB semantics.

Do not describe the feature using SQL assumptions.

---

# Projection Rules

MongoDB projection has inclusion and exclusion modes.

Treat:

```go
Select(...)
```

as inclusion.

Treat:

```go
Exclude(...)
```

as exclusion.

Do not silently combine incompatible projection modes.

When MongoDB has an exception, such as `_id`, preserve actual MongoDB rules rather than inventing ODM rules.

---

# Update Builder Mental Model

An update query has two independent components:

```text
filter
update document
```

Example:

```go
users.
    Where("_id", id).
    Set("name", "Nana").
    Inc("login_count", 1)
```

compiles conceptually to:

```text
filter:
_id == id

update:
$set:
  name: Nana

$inc:
  login_count: 1
```

Query filters and updates must both remain immutable.

---

# Update Operator Rules

Supported fluent operators:

```text
Set       -> $set
Unset     -> $unset
Inc       -> $inc
Push      -> $push
Pull      -> $pull
AddToSet  -> $addToSet
```

Do not rename MongoDB concepts unnecessarily.

Familiar MongoDB naming is preferable to SQL-like abstractions.

---

# Update Composition

This:

```go
Set("first_name", "Nana").
Set("last_name", "Aikinson")
```

must retain both fields.

This:

```go
Inc("views", 1).
Inc("likes", 1)
```

must retain both increments.

Copy maps before mutating derived queries.

Do not share nested update maps between query values.

A shallow struct copy is insufficient if it still shares map/slice backing storage.

---

# Conflicting Updates

Example:

```go
Set("counter", 10).
Inc("counter", 1)
```

may produce a MongoDB-conflicting update.

Do not attempt to implement every server validation rule.

However:

- never silently discard either update,
- do not overwrite one operator with another,
- return early validation errors where the conflict is obvious and easy to detect,
- otherwise allow MongoDB to return the authoritative error.

Preserve the underlying error.

---

# Raw Updates

`UpdateRaw` is an escape hatch.

Treat supplied BSON as opaque native MongoDB update syntax.

Do not convert it into the fluent update representation unless necessary.

Do not reject valid future MongoDB operators simply because Chandlery does not know them.

---

# Update Result Types

Prefer official driver result types when no additional ODM semantics are needed.

Examples:

```go
*mongo.UpdateResult
*mongo.DeleteResult
```

Avoid wrapping these simply for aesthetic reasons.

Create an ODM-specific result type only when Chandlery adds meaningful stable behavior.

---

# UpdateOne vs UpdateMany

Never hide document cardinality.

The public API should make it possible to tell whether an operation affects:

```text
one document
many documents
```

If:

```go
Update(ctx)
```

means update-many, document it.

If:

```go
Update(ctx)
```

means update-one, document it.

Explicit APIs are preferred when ambiguity remains:

```go
UpdateOne(ctx)
UpdateMany(ctx)
DeleteOne(ctx)
DeleteMany(ctx)
```

Do not rely on Eloquent expectations to define MongoDB behavior.

---

# Delete Semantics

Phase 2 deletion is physical deletion.

Do not introduce:

```text
deleted_at
WithTrashed
Restore
ForceDelete
```

until the soft-delete phase.

A query deletion must translate directly to MongoDB deletion behavior.

---

# Validation Philosophy

Validate ODM-level mistakes such as:

```text
unsupported comparison operator
negative limit
negative skip
invalid projection configuration
missing update document
```

Do not rebuild MongoDB's server-side validator.

When MongoDB is the authoritative source for an error, return or wrap that error without obscuring it.

---

# Phase 2 Review Checklist

When reviewing Phase 2 code, specifically inspect for:

- duplicate filter keys being overwritten,
- raw filters being flattened incorrectly,
- shared maps or slices between copied queries,
- update maps shared between derived queries,
- ambiguous update-one/update-many behavior,
- ambiguous delete-one/delete-many behavior,
- SQL-style null assumptions,
- invalid projection mode mixing,
- unsupported operators silently accepted,
- MongoDB-native errors being hidden,
- unnecessary reflection,
- Phase 3 features slipping into the implementation.
