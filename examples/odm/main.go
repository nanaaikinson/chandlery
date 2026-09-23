// Command odm-example walks through the odm package against a real MongoDB:
// models and indexes, CRUD and Save, querying, relationships, cursor
// pagination, soft deletes and transactions, in that order.
//
// It is a console program on purpose. The other examples in this repo are
// HTTP services because the packages they show off are HTTP-shaped; this one
// has nothing to do with HTTP, and a server around it would only be in the
// way.
//
// Run it with a MongoDB 8.0 or later to talk to:
//
//	MONGO_URL=mongodb://localhost:27017 go run ./examples/odm
//
// It writes to a database named odm_example and drops it on the way out.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/nanaaikinson/chandlery/env"
	"github.com/nanaaikinson/chandlery/odm"
)

// Customer embeds odm.Model for an ObjectID _id and timestamps, and
// odm.SoftDeletes so Delete stamps rather than removes.
type Customer struct {
	odm.Model       `bson:",inline"`
	odm.SoftDeletes `bson:",inline"`

	Name     string `bson:"name"`
	Email    string `bson:"email"`
	IsActive bool   `bson:"is_active"`

	// Loaded by With, never stored — that is what bson:"-" is for.
	Orders []Order `bson:"-"`
}

func (Customer) CollectionName() string { return "customers" }

func (Customer) Indexes() []odm.Index {
	return []odm.Index{
		{Keys: bson.D{{Key: "email", Value: 1}}, Unique: true},
		{Keys: bson.D{{Key: "created_at", Value: -1}}},
	}
}

// BeforeCreate runs before the insert and can change what gets written.
func (c *Customer) BeforeCreate(context.Context) error {
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	return nil
}

type Order struct {
	odm.Model `bson:",inline"`

	CustomerID bson.ObjectID `bson:"customer_id"`
	Total      int64         `bson:"total"`
}

func (Order) CollectionName() string { return "orders" }

// CustomerOrders is declared once, next to the models it links.
var CustomerOrders = odm.HasMany[Customer, Order]{
	ForeignKey: "customer_id",
	Attach:     func(c *Customer, orders []Order) { c.Orders = orders },
}

// Active is a scope: reusable filtering, kept out of every call site.
func Active(q *odm.Query[Customer]) *odm.Query[Customer] {
	return q.Where("is_active", true)
}

// auditor watches the model lifecycle from outside the model type.
type auditor struct{}

func (auditor) Created(_ context.Context, c *Customer) error {
	fmt.Printf("  observer: created %s\n", c.Email)
	return nil
}

func (auditor) Updated(_ context.Context, c *Customer) error {
	fmt.Printf("  observer: updated %s\n", c.Email)
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	uri := env.Get("MONGO_URL", "mongodb://localhost:27017")
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", uri, err)
	}
	defer client.Disconnect(ctx)

	// odm.New never opens or closes a client: that lifecycle is the
	// caller's, which is why the Disconnect above is here and not in odm.
	raw := client.Database("odm_example")
	defer raw.Drop(ctx)

	database := odm.New(raw)
	odm.Observe[Customer](database, auditor{})

	customers := odm.Use[Customer](database)
	orders := odm.Use[Order](database)

	for _, step := range []struct {
		name string
		run  func(context.Context, *odm.Collection[Customer], *odm.Collection[Order]) error
	}{
		{"indexes", indexes},
		{"create and save", createAndSave},
		{"querying", querying},
		{"relationships", relationships},
		{"pagination", pagination},
		{"soft deletes", softDeletes},
	} {
		fmt.Printf("\n== %s ==\n", step.name)
		if err := step.run(ctx, customers, orders); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	fmt.Printf("\n== transactions ==\n")
	return transactions(ctx, database, customers, orders)
}

func indexes(ctx context.Context, customers *odm.Collection[Customer], _ *odm.Collection[Order]) error {
	// Safe on every start: nothing is dropped, and an index that already
	// exists unchanged is left alone.
	if err := customers.SyncIndexes(ctx); err != nil {
		return err
	}
	fmt.Println("  synced")
	return nil
}

func createAndSave(ctx context.Context, customers *odm.Collection[Customer], _ *odm.Collection[Order]) error {
	// BeforeCreate lower-cases the email; the ObjectID and timestamps land on
	// the model because Create takes a pointer.
	nana := &Customer{Name: "Nana", Email: "  Nana@Example.com ", IsActive: true}
	if err := customers.Create(ctx, nana); err != nil {
		return err
	}
	fmt.Printf("  created %s (%s)\n", nana.ID, nana.Email)

	// The unique index on email is what makes this ErrDuplicateKey.
	err := customers.Create(ctx, &Customer{Name: "Impostor", Email: "nana@example.com"})
	fmt.Printf("  duplicate email rejected: %t\n", errors.Is(err, odm.ErrDuplicateKey))

	// Save writes only what changed, and nothing at all when nothing did.
	nana.Name = "Nana Kwesi"
	dirty, err := odm.IsDirty(nana, "name")
	if err != nil {
		return err
	}
	if err := customers.Save(ctx, nana); err != nil {
		return err
	}
	fmt.Printf("  saved (name was dirty: %t)\n", dirty)

	if err := customers.Save(ctx, nana); err != nil {
		return err
	}
	fmt.Println("  saving an unchanged model wrote nothing")

	return customers.CreateMany(ctx, []*Customer{
		{Name: "Kwesi", Email: "kwesi@example.com", IsActive: true},
		{Name: "Ama", Email: "ama@example.com"},
	})
}

func querying(ctx context.Context, customers *odm.Collection[Customer], _ *odm.Collection[Order]) error {
	active, err := customers.Scope(Active).OrderBy("name", odm.Asc).Get(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  active: %d\n", len(active))

	// Comparison operators, and two conditions on one field both surviving.
	recent, err := customers.
		Where("created_at", ">=", time.Now().Add(-time.Hour)).
		Where("created_at", "<=", time.Now()).
		Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  created in the last hour: %d\n", recent)

	// An atomic update, with updated_at refreshed for free.
	result, err := customers.Scope(Active).Set("is_active", true).Inc("logins", 1).Update(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  touched %d\n", result.ModifiedCount)

	// Anything the builder doesn't cover stays reachable.
	type byActive struct {
		Active bool  `bson:"_id"`
		Total  int64 `bson:"total"`
	}
	rows, err := odm.AggregateInto[Customer, byActive](ctx, customers.Query(), mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{"_id": "$is_active", "total": bson.M{"$sum": 1}}}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Printf("  is_active=%t: %d\n", row.Active, row.Total)
	}
	return nil
}

func relationships(ctx context.Context, customers *odm.Collection[Customer], orders *odm.Collection[Order]) error {
	all, err := customers.Get(ctx)
	if err != nil {
		return err
	}
	for i, customer := range all {
		if err := orders.CreateMany(ctx, []*Order{
			{CustomerID: customer.ID, Total: int64(100 * (i + 1))},
			{CustomerID: customer.ID, Total: int64(200 * (i + 1))},
		}); err != nil {
			return err
		}
	}

	// One query for the customers, one for every order belonging to any of
	// them — never one per customer.
	loaded, err := customers.With(CustomerOrders).OrderBy("name", odm.Asc).Get(ctx)
	if err != nil {
		return err
	}
	for _, customer := range loaded {
		var total int64
		for _, order := range customer.Orders {
			total += order.Total
		}
		fmt.Printf("  %s: %d orders, %d total\n", customer.Name, len(customer.Orders), total)
	}
	return nil
}

func pagination(ctx context.Context, customers *odm.Collection[Customer], _ *odm.Collection[Order]) error {
	cursor := ""
	for page := 1; ; page++ {
		got, err := customers.
			OrderBy("created_at", odm.Desc).
			CursorPaginate(ctx, odm.CursorPagination{Limit: 2, Cursor: cursor})
		if err != nil {
			return err
		}

		names := make([]string, len(got.Data))
		for i, customer := range got.Data {
			names[i] = customer.Name
		}
		fmt.Printf("  page %d: %s\n", page, strings.Join(names, ", "))

		if !got.HasMore {
			return nil
		}
		cursor = got.NextCursor
	}
}

func softDeletes(ctx context.Context, customers *odm.Collection[Customer], _ *odm.Collection[Order]) error {
	doomed, err := customers.Where("name", "Ama").First(ctx)
	if err != nil {
		return err
	}
	if _, err := customers.Where("_id", doomed.ID).Delete(ctx); err != nil {
		return err
	}

	live, err := customers.Count(ctx)
	if err != nil {
		return err
	}
	trashed, err := customers.OnlyTrashed().Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  live: %d, trashed: %d\n", live, trashed)

	if _, err := customers.Find(ctx, doomed.ID); errors.Is(err, odm.ErrModelNotFound) {
		fmt.Println("  a trashed customer is invisible to a normal read")
	}

	if _, err := customers.Where("_id", doomed.ID).Restore(ctx); err != nil {
		return err
	}
	restored, err := customers.Count(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("  restored, live: %d\n", restored)
	return nil
}

func transactions(ctx context.Context, database *odm.DB, customers *odm.Collection[Customer], orders *odm.Collection[Order]) error {
	rollback := errors.New("changed my mind")

	err := database.Transaction(ctx, func(ctx context.Context) error {
		// Every call inside uses this ctx — it carries the session.
		customer := &Customer{Name: "Rolled back", Email: "rollback@example.com"}
		if err := customers.Create(ctx, customer); err != nil {
			return err
		}
		if err := orders.Create(ctx, &Order{CustomerID: customer.ID, Total: 1}); err != nil {
			return err
		}
		return rollback
	})

	switch {
	case errors.Is(err, rollback):
		gone, countErr := customers.Where("email", "rollback@example.com").Exists(ctx)
		if countErr != nil {
			return countErr
		}
		fmt.Printf("  rolled back, customer exists: %t\n", gone)
		return nil
	case err != nil:
		// Transactions need a replica set; a standalone server says so.
		fmt.Fprintf(os.Stderr, "  skipped: %v\n", err)
		return nil
	default:
		return errors.New("the transaction committed when it should have rolled back")
	}
}
