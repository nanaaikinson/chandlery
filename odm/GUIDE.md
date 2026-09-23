# odm — a guide

Everything this package does, in the order you'd meet it, worked through one
application: a multi-tenant invoicing service. Businesses have customers,
customers get invoices, invoices get paid, and staff hold roles.

For the terse version, see [README.md](README.md). For compile-checked
snippets, `go doc github.com/nanaaikinson/chandlery/odm`. For a program you
can run, [`examples/odm`](../examples/odm).

- [1. Setting up](#1-setting-up)
- [2. Modelling](#2-modelling)
- [3. Reading](#3-reading)
- [4. Writing](#4-writing)
- [5. Save and dirty tracking](#5-save-and-dirty-tracking)
- [6. Deleting](#6-deleting)
- [7. Scopes](#7-scopes)
- [8. Hooks and observers](#8-hooks-and-observers)
- [9. Pagination](#9-pagination)
- [10. Aggregation](#10-aggregation)
- [11. Relationships](#11-relationships)
- [12. Transactions](#12-transactions)
- [13. Indexes](#13-indexes)
- [14. Errors](#14-errors)
- [15. Testing](#15-testing)
- [16. Escape hatches](#16-escape-hatches)
- [17. Things that bite](#17-things-that-bite)

---

## 1. Setting up

```
go get github.com/nanaaikinson/chandlery
```

Needs Go 1.26.3+, [mongo-driver/v2](https://go.mongodb.org/mongo-driver/v2)
and **MongoDB 8.0 or later**.

`odm.New` wraps a database you already connected. It never opens a client and
never closes one — that lifecycle stays yours, which is what makes this
package safe to use from a test, a worker and a server in the same process.

```go
func main() {
	ctx := context.Background()

	client, err := mongo.Connect(options.Client().ApplyURI(os.Getenv("MONGO_URL")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)

	database := odm.New(client.Database("invoicing"))

	// Collections are cheap, immutable and safe to share. Build them once
	// at startup and hand them to whatever needs them.
	app := &App{
		businesses: odm.Use[Business](database),
		customers:  odm.Use[Customer](database),
		invoices:   odm.Use[Invoice](database),
		payments:   odm.Use[Payment](database),
	}

	if err := app.customers.SyncIndexes(ctx); err != nil {
		log.Fatal(err)
	}
	...
}
```

There is no global state and no default connection. A second database — a
test's, a tenant's, a replica for reporting — is a second `odm.New`, carrying
its own observers and its own clock.

---

## 2. Modelling

A model is a plain struct. What it embeds decides what it gets.

```go
type Customer struct {
	odm.Model       `bson:",inline"`
	odm.SoftDeletes `bson:",inline"`

	BusinessID string `bson:"business_id"`
	Name       string `bson:"name"`
	Email      string `bson:"email"`
	Phone      string `bson:"phone,omitempty"`
	IsActive   bool   `bson:"is_active"`

	// Loaded by With, never stored.
	Invoices []Invoice `bson:"-"`
}

func (Customer) CollectionName() string { return "customers" }
```

| Embed | You get |
| --- | --- |
| nothing | a plain document; supply your own `_id` or let MongoDB generate one |
| `odm.IdentityModel` | an ObjectID `_id` assigned on insert, and dirty tracking |
| `odm.Model` | the above plus `created_at`/`updated_at`, stamped and refreshed |
| `odm.SoftDeletes` | `Delete` stamps `deleted_at`; reads hide those documents |

**A different `_id` type.** Declare your own `ID` next to the embed and it
shadows the embedded ObjectID — in Go and in the BSON codec alike — while the
timestamps and dirty tracking stay. Assign it in `BeforeCreate`; once
shadowed, `Create` no longer generates one:

```go
type Business struct {
	odm.Model `bson:",inline"`
	ID        string `bson:"_id" json:"id"`
}

func (b *Business) BeforeCreate(ctx context.Context) error {
	if b.ID == "" {
		b.ID = ulid.Make().String()
	}
	return nil
}
```

**`bson:",inline"` is not optional.** Without it the embedded struct nests
under its own lowercased name, so the document has no top-level `_id` — and
every query still compiles. [`TestConformance`](#15-testing) catches this.

**Collection names.** `CollectionName()` wins. Without it the name is the
lowercased type name plus `"s"` — `Invoice` → `invoices`. That is the whole
of the pluralization, so `Company` would become `companys`: spell it out when
the naive rule is wrong.

**Relation fields need `bson:"-"`.** They are loaded, not stored. Leave the
tag off and you persist a copy of every invoice inside the customer.

---

## 3. Reading

Everything starts from a collection and ends in a terminal that takes a
`context.Context`.

```go
customer, err := customers.Where("email", email).First(ctx)
if errors.Is(err, odm.ErrModelNotFound) {
	return nil, ErrNoSuchCustomer
} else if err != nil {
	return nil, err
}
```

### Conditions

Conditions AND together, including two on the same field:

```go
invoices, err := invoices.
	Where("business_id", businessID).
	Where("status", "!=", "draft").
	Where("total", ">=", 10_00).
	Where("total", "<=", 500_00).         // both bounds survive
	WhereIn("currency", []string{"GHS", "USD"}).
	WhereNotIn("status", []string{"void", "written_off"}).
	WhereNotNull("issued_at").
	WhereBetween("issued_at", from, to).
	Get(ctx)
```

| Method | MongoDB |
| --- | --- |
| `Where(f, v)` | `{f: v}` |
| `Where(f, op, v)` | `$ne`, `$gt`, `$gte`, `$lt`, `$lte` — or plain equality for `=`/`==` |
| `WhereIn(f, vals)` / `WhereNotIn(f, vals)` | `$in` / `$nin` |
| `WhereNull(f)` / `WhereNotNull(f)` | `{f: null}` / `{f: {$ne: null}}` |
| `WhereBetween(f, a, b)` | `$gte` + `$lte`, inclusive |
| `WhereNotBetween(f, a, b)` | `$or` of `$lt` / `$gt` |
| `WhereRaw(filter)` | anything, ANDed with the rest |

Operators come in two spellings. Both compile to the same filter; the typed
one is checked by the compiler:

```go
invoices.Where("total", ">=", 100)      // clear at a call site
invoices.Where("total", odm.Gte, 100)   // a typo won't build
```

### OR

`OrWhere` ORs with **everything accumulated before it**, folded strictly left
to right with no precedence:

```go
q.Where(a).OrWhere(b)            // a OR b
q.Where(a).Where(b).OrWhere(c)   // (a AND b) OR c
q.Where(a).OrWhere(b).Where(c)   // (a OR b) AND c   ← not SQL's precedence
```

For a grouped predicate, write the `$or` yourself:

```go
customers.
	Where("business_id", businessID).
	WhereRaw(bson.M{"$or": bson.A{
		bson.M{"email": term},
		bson.M{"phone": term},
	}}).
	Get(ctx)
```

### Ordering, limiting, projecting

```go
recent, err := invoices.
	Where("business_id", businessID).
	OrderBy("issued_at", odm.Desc).
	OrderBy("number", odm.Asc).       // breaks ties from the first
	Select("number", "total", "status", "issued_at").
	Limit(50).
	Get(ctx)
```

`Latest()` and `Oldest()` are `OrderBy` on `created_at` by default, or on
fields you name: `Latest("issued_at")`.

Projected-away fields decode as their zero value — a projected model is a
partial one. MongoDB won't mix inclusions and exclusions (bar `_id`), and the
builder rejects an illegal mixture rather than letting the server do it.

### Terminals

| Terminal | Returns |
| --- | --- |
| `Get(ctx)` | `[]T` — empty is not an error |
| `First(ctx)` | `T` — missing is `odm.ErrModelNotFound` |
| `Find(ctx, id)` | `T` by `_id`, of whatever type your model uses |
| `Count(ctx)` | `int64` — ignores `Limit`/`Skip`, so it's the real total |
| `Exists(ctx)` | `bool` — stops at the first match, decodes nothing |

### Queries are values

Every builder method returns a new query. A partially built one is safe to
keep, branch from and share across goroutines:

```go
// One base, three reports, no interference.
base := invoices.Where("business_id", businessID).Where("issued_at", ">=", monthStart)

overdue, err := base.Where("status", "overdue").Count(ctx)
paid, err := base.Where("status", "paid").Count(ctx)
total, err := base.Count(ctx)
```

---

## 4. Writing

### Creating

```go
customer := Customer{
	BusinessID: businessID,
	Name:       "Nana",
	Email:      "nana@example.com",
	IsActive:   true,
}

// Takes a pointer, so the ObjectID and timestamps land on your variable.
if err := customers.Create(ctx, &customer); err != nil {
	return err
}
fmt.Println(customer.ID, customer.CreatedAt)
```

`CreateMany` is one round trip with the same preparation per model:

```go
err := customers.CreateMany(ctx, []*Customer{
	{Name: "Nana", Email: "nana@example.com"},
	{Name: "Kwesi", Email: "kwesi@example.com"},
})
```

It is MongoDB's `InsertMany`, not a transaction: by default it stops at the
first failing document and the ones before it stay written. Pass
`options.InsertMany().SetOrdered(false)` to keep going, or wrap it in a
[transaction](#12-transactions).

### Atomic updates

Updates compile to MongoDB's own operators — no read-modify-write, no lost
updates between concurrent writers:

```go
result, err := invoices.
	Where("_id", invoiceID).
	Where("status", "pending").        // guard: only if still pending
	Set("status", "paid").
	Set("paid_at", time.Now()).
	Inc("payment_count", 1).
	Push("events", "paid").
	Update(ctx)

if result.MatchedCount == 0 {
	return ErrAlreadySettled          // the guard held
}
```

| Method | Operator |
| --- | --- |
| `Set` / `Unset` | `$set` / `$unset` — unset removes the field, which isn't nulling it |
| `Inc` / `Increment` / `Decrement` | `$inc` |
| `Push` / `Pull` / `AddToSet` | `$push` / `$pull` / `$addToSet` |

Repeated calls on one operator group together. A field may be targeted only
once per update — MongoDB's own rule — so `Set("a", 1).Inc("a", 1)` is
refused here rather than at the server.

`Update` is `UpdateMany`. `UpdateOne` stops at one document and lets
`OrderBy` choose which — the claim-a-job pattern:

```go
// A worker taking the oldest unclaimed invoice, atomically.
result, err := invoices.
	Where("status", "pending").
	Oldest("issued_at").
	Set("status", "processing").
	Set("claimed_by", workerID).
	UpdateOne(ctx)
```

`updated_at` is refreshed automatically on a model embedding `odm.Model`.
`WithoutTimestamps()` suppresses it for a backfill or a counter bump that
shouldn't count as activity.

### Raw updates

```go
_, err := invoices.Where("_id", id).UpdateRaw(ctx, bson.M{
	"$set":  bson.M{"status": "paid"},
	"$push": bson.M{"tags": bson.M{"$each": []string{"paid", "reconciled"}}},
})
```

Passed straight to the driver — `bson.M`, `bson.D`, or an aggregation
pipeline. Nothing re-validates it, nothing is stamped, and it refuses to run
alongside staged operators rather than silently dropping them.

---

## 5. Save and dirty tracking

A model that came from the database remembers what it looked like then, so
`Save` writes only the difference.

```go
customer, err := customers.Find(ctx, id)
if err != nil {
	return err
}

customer.Name = req.Name
customer.Phone = req.Phone

if err := customers.Save(ctx, &customer); err != nil {
	return err
}
```

This is the API to reach for in an HTTP handler that patches a record. Four
things make it safe:

- **It is a `$set` of changes, never a whole-document replacement.** A field
  another writer changed meanwhile survives untouched unless you changed it
  too.
- **An unchanged model is not written at all** — no round trip, no
  `updated_at`, no hooks, no observers.
- **A field your struct doesn't declare is never touched.** The snapshot is
  of the model, not the document, so a column from an older schema or another
  service can't end up in an `$unset`.
- Insert or update comes from whether the model **is persisted**, never from
  whether its ID looks set.

```go
odm.IsPersisted(&customer)              // did this come from the database?
odm.IsDirty(&customer)                  // anything unsaved?
odm.IsDirty(&customer, "email")         // that field in particular?
odm.Changes(&customer)                  // (odm.Changeset, error)
odm.Original(&customer, "email")        // the value before the change
odm.WasChanged(&customer, "email")      // did the last Save write it?
```

`IsDirty` and `WasChanged` are the same question in two tenses. After a
`Save`, `Changes` is empty and `Original` has moved on, so `WasChanged` is
the one that can still answer:

```go
if err := customers.Save(ctx, &customer); err != nil {
	return err
}
if odm.WasChanged(&customer, "email") {
	return mailer.ConfirmAddress(ctx, customer.Email)
}
```

An `omitempty` field falling to its zero value is an `$unset`, not a
`$set` of empty — that's the difference between "empty" and "absent", and
it's deliberate.

---

## 6. Deleting

Without `odm.SoftDeletes`, `Delete` is a physical MongoDB delete:

```go
result, err := invoices.Where("status", "draft").Delete(ctx)     // DeleteMany
result, err := invoices.Where("_id", id).DeleteOne(ctx)          // at most one
```

With it, `Delete` stamps `deleted_at` and every read on the collection skips
those documents:

```go
customers.Get(ctx)                              // live only
customers.WithTrashed().Get(ctx)                // live and deleted
customers.OnlyTrashed().Get(ctx)                // deleted only

customers.Where("_id", id).Restore(ctx)         // clears deleted_at
customers.Where("_id", id).ForceDelete(ctx)     // removes it for real
```

The scope is applied at compile time, so `WithTrashed` overrides it wherever
it appears in the chain. Two behaviours worth knowing:

- **`ForceDelete` ignores the default scope.** Purging is why you reach for
  it, and skipping the already-trashed rows would be the surprise.
  `OnlyTrashed().ForceDelete(ctx)` narrows it to just those.
- **`Restore` only ever touches soft-deleted documents.** `WithTrashed` and
  `OnlyTrashed` make no difference to it.

`DeletedAt` is a `*time.Time` with `omitempty`, so a live document carries no
`deleted_at` field at all. MongoDB's `{deleted_at: null}` matches a missing
field too, so a collection that gains soft deletes later needs no backfill.

---

## 7. Scopes

A scope is a named query transformation — an ordinary function over an
immutable query. No context, no error return, so it *cannot* perform I/O:

```go
func Active(q *odm.Query[Customer]) *odm.Query[Customer] {
	return q.Where("is_active", true)
}

func ForBusiness(businessID string) odm.Scope[Customer] {
	return func(q *odm.Query[Customer]) *odm.Query[Customer] {
		return q.Where("business_id", businessID)
	}
}
```

The second form is the one that earns its keep in a multi-tenant app —
tenant scoping stops being something every call site has to remember:

```go
func (a *App) customersFor(businessID string) *odm.Query[Customer] {
	return a.customers.Scope(ForBusiness(businessID), Active)
}

// Every read downstream is scoped by construction.
list, err := a.customersFor(businessID).OrderBy("name", odm.Asc).Get(ctx)
one, err := a.customersFor(businessID).Find(ctx, customerID)
```

---

## 8. Hooks and observers

**Hooks** live on the model, for behaviour that belongs to the document:

```go
func (c *Customer) BeforeCreate(context.Context) error {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if c.Email == "" {
		return errors.New("customer: email is required")
	}
	return nil
}
```

| Hook | When |
| --- | --- |
| `BeforeCreate` | before an insert; an error aborts it |
| `AfterCreate` | after it succeeds — cannot undo the write |
| `BeforeUpdate` | before `Save` writes; may change the model, and the update is recomputed after it |
| `AfterUpdate` | after `Save`'s write succeeds |

**Observers** are the same events outside the model type, for behaviour that
belongs to the application:

```go
type CustomerObserver struct{ mailer *Mailer }

func (o CustomerObserver) Created(ctx context.Context, c *Customer) error {
	return o.mailer.Welcome(ctx, c.Email)
}

func (o CustomerObserver) Updating(ctx context.Context, c *Customer) error {
	// Read what is about to change, before the write happens.
	changed, err := odm.IsDirty(c, "email")
	if err != nil {
		return err
	}
	if changed {
		return o.mailer.VerifyNewAddress(ctx, c.Email)
	}
	return nil
}

odm.Observe[Customer](database, CustomerObserver{mailer: mailer})
```

Each event is its own interface, so implement only what you need. Registration
is **per database**, not per process. The model's hook runs first, then
observers in registration order; the first error stops the rest and aborts the
write for the `-ing` events.

Hooks fire only where a model instance exists — `Create`, `CreateMany` and
`Save`. A query-level `Update` or `Delete` acts on documents it never
hydrates, so there is nothing to hand a hook. That is also why there is no
`BeforeDelete`.

---

## 9. Pagination

Cursor pagination seeks to a page rather than counting past everything before
it, so page 900 costs what page 1 does:

```go
func (a *App) ListInvoices(w http.ResponseWriter, r *http.Request) {
	page, err := a.invoices.
		Scope(ForBusiness(businessIDFrom(r))).
		OrderBy("issued_at", odm.Desc).
		CursorPaginate(r.Context(), odm.CursorPagination{
			Limit:  20,
			Cursor: r.URL.Query().Get("cursor"),
		})

	switch {
	case errors.Is(err, odm.ErrInvalidCursor):
		http.Error(w, "bad cursor", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// CursorPage is already JSON-shaped: {data, next_cursor, has_more}.
	json.NewEncoder(w).Encode(page)
}
```

`_id` is appended to the sort as a tie-breaker unless it's already there.
That is not a detail: documents sharing an `issued_at` have no order between
them, and a cursor on `issued_at` alone silently **skips and repeats rows**
across page boundaries.

The cursor is opaque — base64 of a small BSON document holding the sort it
was minted under and the last row's values. Hand one to a differently-sorted
query and you get `ErrInvalidCursor` rather than nonsense.

The query's own `Limit`/`Skip` are ignored. A projection must keep every
field the sort uses, since the next cursor is read out of the rows returned.

---

## 10. Aggregation

The pipeline is the driver's own, passed through untouched:

```go
type RevenueByMonth struct {
	Month string  `bson:"_id"`
	Total float64 `bson:"total"`
	Count int64   `bson:"count"`
}

rows, err := odm.AggregateInto[Invoice, RevenueByMonth](ctx,
	invoices.Scope(ForBusiness(businessID)).Where("status", "paid"),
	mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   bson.M{"$dateToString": bson.M{"format": "%Y-%m", "date": "$paid_at"}},
			"total": bson.M{"$sum": "$total"},
			"count": bson.M{"$sum": 1},
		}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	},
)
```

The query's filter is prepended as a `$match`, so an aggregation is scoped
the same way every other read on that query is — soft deletes included.
`Query.Aggregate` decodes into the model; `AggregateInto` decodes into
anything else, which is the usual case for a `$group`.

The cost is that this package's `$match` is the first stage the server sees.
A pipeline that must open with `$geoNear`, `$changeStream` or `$indexStats`
needs `Raw().Aggregate`, which prepends nothing and scopes nothing.

---

## 11. Relationships

Declare the link once, next to the models. `Attach` is a function rather
than a field name found by reflection — the compiler checks it.

```go
type Invoice struct {
	odm.Model `bson:",inline"`

	BusinessID string        `bson:"business_id"`
	CustomerID bson.ObjectID `bson:"customer_id"`
	Number     string        `bson:"number"`
	Total      int64         `bson:"total"`
	Status     string        `bson:"status"`

	Customer *Customer `bson:"-"`
	Payments []Payment `bson:"-"`
}

var (
	InvoiceCustomer = odm.BelongsTo[Invoice, Customer]{
		ForeignKey: "customer_id",
		Attach:     func(i *Invoice, c *Customer) { i.Customer = c },
	}
	InvoicePayments = odm.HasMany[Invoice, Payment]{
		ForeignKey: "invoice_id",
		Attach:     func(i *Invoice, p []Payment) { i.Payments = p },
	}
	CustomerInvoices = odm.HasMany[Customer, Invoice]{
		ForeignKey: "customer_id",
		Attach:     func(c *Customer, i []Invoice) { c.Invoices = i },
	}
)
```

| Declaration | Where the key lives | `Attach` receives |
| --- | --- | --- |
| `HasMany[T, R]` | on the related model | `[]R`, empty when there are none |
| `HasOne[T, R]` | on the related model | `*R`, nil when there is none |
| `BelongsTo[T, R]` | on this model | `*R`, nil when unset or dangling |
| `BelongsToMany[T, R]` | on this model, as a list | `[]R`, empty when there are none |

**Give a key the same type as the `_id` it points at.** Keys are matched by
BSON type as well as value. `odm.Model`'s default `_id` is a `bson.ObjectID`,
so a key pointing at one is `bson.ObjectID` (or `[]bson.ObjectID` for a
many-to-many list) — a `string` holding the hex form would never match. A
model that overrides its `_id` to a `string` takes `string` keys instead.

Load them with `With`:

```go
list, err := invoices.
	Scope(ForBusiness(businessID)).
	With(InvoiceCustomer, InvoicePayments).
	Get(ctx)
```

**Loading batches, always.** Each relation is one extra query whatever the
number of parents — the keys are collected and the related documents fetched
with a single `$in`. Two relations over a hundred invoices is three queries,
not two hundred and one.

Relations nest, and the levels stay batched:

```go
// customers → invoices → payments: three queries.
customers.With(CustomerInvoices.With(InvoicePayments)).Get(ctx)
```

`With` copies the declaration rather than changing it, so one exported
relation works both plain and nested.

### Many-to-many

MongoDB stores this as a list of ids, not a join collection — the list *is*
the relationship:

```go
type StaffMember struct {
	odm.Model `bson:",inline"`

	RoleIDs []bson.ObjectID `bson:"role_ids"`
	Roles   []Role          `bson:"-"`
}

var StaffRoles = odm.BelongsToMany[StaffMember, Role]{
	LocalKey: "role_ids",
	Attach:   func(s *StaffMember, r []Role) { s.Roles = r },
}
```

The other direction needs no new declaration — where the list lives on the
*related* model, that's a `HasMany` whose foreign key holds an array:

```go
var RoleStaff = odm.HasMany[Role, StaffMember]{
	ForeignKey: "role_ids",
	Attach:     func(r *Role, s []StaffMember) { r.Staff = s },
}
```

A join collection carrying its own fields (`granted_at`, `granted_by`) is a
model of its own with two `BelongsTo` relations — nothing here needed.

Nothing loads on field access. Reading `invoice.Customer` reads a struct
field, never a query.

---

## 12. Transactions

```go
err := database.Transaction(ctx, func(ctx context.Context) error {
	if err := payments.Create(ctx, &payment); err != nil {
		return err
	}

	result, err := invoices.
		Where("_id", payment.InvoiceID).
		Where("status", "pending").
		Set("status", "paid").
		Update(ctx)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return ErrAlreadySettled      // returning an error rolls everything back
	}
	return nil
})
```

**The context handed to the callback *is* the transaction.** It carries the
session, so every call inside must use *that* `ctx` — one using the outer
`ctx` runs outside the transaction and commits on its own, silently. That is
also why there's no transactional collection type to build: the collections
you already have take part by being given this context.

Two things, both MongoDB's rather than this package's:

- Transactions need a replica set or sharded cluster.
- **The callback can run more than once.** This uses the driver's
  `WithTransaction`, which retries on a transient error and retries a commit
  whose outcome is unknown. Keep the callback idempotent, and keep anything
  that isn't a database write — sending mail, charging a card — outside it.

---

## 13. Indexes

```go
func (Customer) Indexes() []odm.Index {
	return []odm.Index{
		{
			Keys:   bson.D{{Key: "business_id", Value: 1}, {Key: "email", Value: 1}},
			Unique: true,
		},
		{Keys: bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{
			// Partial, so the unique index above doesn't count trashed rows.
			Keys:          bson.D{{Key: "phone", Value: 1}},
			Sparse:        true,
			PartialFilter: bson.M{"deleted_at": nil},
		},
	}
}

err := customers.SyncIndexes(ctx)
```

`Index` also carries `Name` and `ExpireAfter` (a TTL index). Anything beyond
that — collation, wildcard, text or geo — belongs on `Raw().Indexes()`.

`SyncIndexes` is safe on every start: creating an index that already exists
unchanged does nothing. It **only ever adds**. An index no longer declared is
left alone, and one whose declaration changed reports MongoDB's own conflict
rather than being quietly rebuilt — dropping an index should be a deliberate
act, not a deploy side effect.

---

## 14. Errors

Match with `errors.Is`, never on the message.

| Sentinel | Means |
| --- | --- |
| `odm.ErrModelNotFound` | `First`/`Find` matched nothing; wraps `mongo.ErrNoDocuments` |
| `odm.ErrDuplicateKey` | a write violated a unique index; wraps `*mongo.WriteException` |
| `odm.ErrInvalidCursor` | `CursorPaginate` got a cursor it can't use |
| `odm.ErrInvalidQuery` | a builder call was handed invalid input |
| `odm.ErrNilModel` | `Create`/`Save` got a nil pointer |

The driver's error stays wrapped underneath, so the detail is still there:

```go
if err := customers.Create(ctx, &customer); errors.Is(err, odm.ErrDuplicateKey) {
	var write mongo.WriteException
	if errors.As(err, &write) {
		log.Printf("constraint: %s", write.WriteErrors[0].Message)
	}
	return ErrEmailTaken
}
```

`ErrInvalidQuery` is deferred: builder methods return `*Query[T]` and have
nowhere to put an error, so the query records the first failure and the next
terminal returns it.

---

## 15. Testing

**Check your models** with the exported conformance suite. It catches the
quiet mistakes — a missing `bson:",inline"`, two fields tagged to the same
name, a relation field that isn't `bson:"-"`:

```go
func TestCustomerModel(t *testing.T) {
	odm.TestConformance(t, odm.Use[Customer](database), func() *Customer {
		return &Customer{Name: "Nana", Email: "nana@example.com"}
	})
}
```

It adapts to what the model embeds. Give it a collection of its own.

**Freeze the clock** so timestamps are assertable:

```go
at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
database := odm.New(raw, odm.WithClock(func() time.Time { return at }))
```

**Use a real MongoDB.** Testcontainers over a fake: the behaviours this
package leans on — null versus missing, `$nin` on an empty list, index
conflicts, transaction rollback — are exactly the ones a fake gets wrong.

---

## 16. Escape hatches

Nothing here traps you.

```go
database.Raw()                        // *mongo.Database — RunCommand, GridFS, change streams
customers.Raw()                       // *mongo.Collection — FindOneAndUpdate, index management
customers.WhereRaw(bson.M{...})       // any filter
customers.Where(...).UpdateRaw(ctx, bson.M{...})   // any update, or a pipeline
customers.Raw().Aggregate(ctx, pipeline)           // no $match prepended, no scope applied
customers.BulkWrite(ctx, []mongo.WriteModel{...})  // passed straight through
```

`BulkWrite` rewrites nothing on the way through: no soft-delete scope, no
timestamps, no hooks. A bulk write is a batch of instructions you composed.

---

## 17. Things that bite

Ordered by how likely they are to catch you.

**`bson:",inline"` on embedded bases.** Leave it off and `_id` nests under
`model`. Everything compiles; nothing works. Run `TestConformance`.

**Relation fields need `bson:"-"`.** Otherwise you persist a copy of every
related document inside the parent.

**`OrWhere` is not SQL precedence.** `Where(a).OrWhere(b).Where(c)` is
`(a OR b) AND c`, not `a OR (b AND c)`. Grouped predicates need `WhereRaw`.

**`WhereNull` matches null *and missing*.** That's MongoDB, not a shortcut.
For "present and explicitly null", use
`WhereRaw(bson.M{f: bson.M{"$type": "null"}})`.

**`WhereNotIn` with an empty slice matches everything.** `$nin: []` excludes
nothing, the mirror of `$in: []` matching nothing. Guard the empty case if
that's not what you want.

**Cursor pagination needs the sort's fields in the projection**, because the
next cursor is read out of the rows returned.

**`ForceDelete` ignores the soft-delete scope** — by design, since purging is
the point. `Restore` only touches trashed rows.

**A no-op `Save` writes nothing** — including no `updated_at`. If you need a
heartbeat, write it explicitly.

**`Update` is `UpdateMany`.** An unfiltered query hits the whole collection.
That's why the mutating terminals live on `Query` and not `Collection`:
clearing everything takes an explicit `customers.Query().Delete(ctx)`.

**Transactions: use the callback's `ctx`.** The outer one escapes the
transaction silently.

**`Aggregate` prepends a `$match`.** Pipelines needing a first-stage-only
operator must use `Raw().Aggregate`.

**Models from `Aggregate` are not tracked.** A grouped row isn't a document,
so `Save` would insert it.
