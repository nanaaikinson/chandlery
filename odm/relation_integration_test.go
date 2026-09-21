//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/nanaaikinson/chandlery/odm"
)

type customer struct {
	odm.Model `bson:",inline"`

	Name string `bson:"name"`

	// Loaded, not stored — see the bson:"-" test below.
	Orders  []purchase `bson:"-"`
	Profile *profile   `bson:"-"`
}

func (customer) CollectionName() string { return "customers" }

type purchase struct {
	odm.Model `bson:",inline"`

	CustomerID string `bson:"customer_id"`
	Total      int    `bson:"total"`

	Customer *customer `bson:"-"`
}

func (purchase) CollectionName() string { return "purchases" }

type profile struct {
	odm.Model `bson:",inline"`

	CustomerID string `bson:"customer_id"`
	Tier       string `bson:"tier"`
}

func (profile) CollectionName() string { return "profiles" }

var (
	customerOrders = odm.HasMany[customer, purchase]{
		ForeignKey: "customer_id",
		Attach:     func(c *customer, orders []purchase) { c.Orders = orders },
	}
	customerProfile = odm.HasOne[customer, profile]{
		ForeignKey: "customer_id",
		Attach:     func(c *customer, p *profile) { c.Profile = p },
	}
	purchaseCustomer = odm.BelongsTo[purchase, customer]{
		ForeignKey: "customer_id",
		Attach:     func(p *purchase, c *customer) { p.Customer = c },
	}
)

// shop wires the three collections onto one database.
type shop struct {
	customers *odm.Collection[customer]
	purchases *odm.Collection[purchase]
	profiles  *odm.Collection[profile]
}

func newShop(t *testing.T) shop {
	t.Helper()

	return shopOn(t, odm.New(testDatabase(t, client)))
}

func shopOn(t *testing.T, database *odm.DB) shop {
	t.Helper()

	return shop{
		customers: odm.Use[customer](database),
		purchases: odm.Use[purchase](database),
		profiles:  odm.Use[profile](database),
	}
}

func create[T any](t *testing.T, collection *odm.Collection[T], models ...*T) {
	t.Helper()

	for _, model := range models {
		if err := collection.Create(context.Background(), model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
}

func totals(orders []purchase) []int {
	out := make([]int, len(orders))
	for i, order := range orders {
		out[i] = order.Total
	}
	return out
}

func TestHasMany(t *testing.T) {
	t.Parallel()

	t.Run("groups the related documents onto the right parents", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)

		nana := &customer{Name: "nana"}
		kwesi := &customer{Name: "kwesi"}
		lonely := &customer{Name: "lonely"}
		create(t, s.customers, nana, kwesi, lonely)
		create(t, s.purchases,
			&purchase{CustomerID: nana.ID, Total: 10},
			&purchase{CustomerID: kwesi.ID, Total: 20},
			&purchase{CustomerID: nana.ID, Total: 30},
		)

		got, err := s.customers.With(customerOrders).OrderBy("name", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("Get() returned %d customers, want 3", len(got))
		}

		byName := map[string][]int{}
		for _, c := range got {
			byName[c.Name] = totals(c.Orders)
		}
		if want := []int{10, 30}; !reflect.DeepEqual(byName["nana"], want) {
			t.Errorf("nana's orders = %v, want %v", byName["nana"], want)
		}
		if want := []int{20}; !reflect.DeepEqual(byName["kwesi"], want) {
			t.Errorf("kwesi's orders = %v, want %v", byName["kwesi"], want)
		}
		if got := byName["lonely"]; len(got) != 0 {
			t.Errorf("lonely's orders = %v, want none", got)
		}
	})

	t.Run("a parent with no related documents gets an empty slice, not nil", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)
		create(t, s.customers, &customer{Name: "lonely"})

		got, err := s.customers.With(customerOrders).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got[0].Orders == nil {
			t.Error("Orders = nil, want an empty slice")
		}
		if len(got[0].Orders) != 0 {
			t.Errorf("Orders = %v, want none", got[0].Orders)
		}
	})

	t.Run("no related documents at all still attaches", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)
		create(t, s.customers, &customer{Name: "a"}, &customer{Name: "b"})

		got, err := s.customers.With(customerOrders).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		for _, c := range got {
			if c.Orders == nil {
				t.Errorf("%q kept a nil Orders", c.Name)
			}
		}
	})
}

func TestHasOne(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newShop(t)

	withProfile := &customer{Name: "with"}
	without := &customer{Name: "without"}
	create(t, s.customers, withProfile, without)
	create(t, s.profiles, &profile{CustomerID: withProfile.ID, Tier: "gold"})

	got, err := s.customers.With(customerProfile).OrderBy("name", odm.Asc).Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got[0].Profile == nil || got[0].Profile.Tier != "gold" {
		t.Errorf("first customer's profile = %v, want the gold one", got[0].Profile)
	}
	if got[1].Profile != nil {
		t.Errorf("second customer's profile = %v, want nil", got[1].Profile)
	}
}

func TestBelongsTo(t *testing.T) {
	t.Parallel()

	t.Run("loads the owning document", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)

		nana := &customer{Name: "nana"}
		create(t, s.customers, nana)
		create(t, s.purchases,
			&purchase{CustomerID: nana.ID, Total: 10},
			&purchase{CustomerID: nana.ID, Total: 20},
		)

		got, err := s.purchases.With(purchaseCustomer).OrderBy("total", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		for _, order := range got {
			if order.Customer == nil || order.Customer.Name != "nana" {
				t.Errorf("order %d's customer = %v, want nana", order.Total, order.Customer)
			}
		}
	})

	t.Run("a dangling key reads as absent", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)
		create(t, s.purchases, &purchase{CustomerID: "no-such-customer", Total: 10})

		got, err := s.purchases.With(purchaseCustomer).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got[0].Customer != nil {
			t.Errorf("Customer = %v, want nil for a dangling key", got[0].Customer)
		}
	})

	t.Run("an unset key reads as absent", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		s := newShop(t)
		create(t, s.purchases, &purchase{Total: 10})

		got, err := s.purchases.With(purchaseCustomer).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got[0].Customer != nil {
			t.Errorf("Customer = %v, want nil", got[0].Customer)
		}
	})
}

func TestEagerLoadingBatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// A client of this suite's own, watching every command it sends, so the
	// N+1 claim is counted rather than assumed.
	var mu sync.Mutex
	finds := map[string]int{}
	monitor := &event.CommandMonitor{
		Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if e.CommandName != "find" {
				return
			}
			collection, ok := e.Command.Lookup("find").StringValueOK()
			if !ok {
				return
			}
			mu.Lock()
			finds[collection]++
			mu.Unlock()
		},
	}

	watched, err := mongo.Connect(options.Client().ApplyURI(primaryURI).SetMonitor(monitor))
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { watched.Disconnect(context.Background()) })

	s := shopOn(t, odm.New(testDatabase(t, watched)))

	const customers = 10
	for i := range customers {
		owner := &customer{Name: string(rune('a' + i))}
		create(t, s.customers, owner)
		create(t, s.purchases,
			&purchase{CustomerID: owner.ID, Total: i},
			&purchase{CustomerID: owner.ID, Total: i * 2},
		)
	}

	mu.Lock()
	finds = map[string]int{}
	mu.Unlock()

	got, err := s.customers.With(customerOrders, customerProfile).Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(got) != customers {
		t.Fatalf("Get() returned %d customers, want %d", len(got), customers)
	}
	for _, c := range got {
		if len(c.Orders) != 2 {
			t.Fatalf("%q has %d orders, want 2", c.Name, len(c.Orders))
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// One for the parents, one per relation — never one per parent.
	if finds["customers"] != 1 || finds["purchases"] != 1 || finds["profiles"] != 1 {
		t.Errorf("find commands = %v, want exactly one per collection over %d parents", finds, customers)
	}
}

func TestEagerLoadingOnOtherReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newShop(t)

	nana := &customer{Name: "nana"}
	create(t, s.customers, nana)
	create(t, s.purchases,
		&purchase{CustomerID: nana.ID, Total: 10},
		&purchase{CustomerID: nana.ID, Total: 20},
	)

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		got, err := s.customers.With(customerOrders).First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if want := []int{10, 20}; !reflect.DeepEqual(totals(got.Orders), want) {
			t.Errorf("orders = %v, want %v", totals(got.Orders), want)
		}
	})

	t.Run("Find", func(t *testing.T) {
		t.Parallel()

		got, err := s.customers.With(customerOrders).Find(ctx, nana.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if len(got.Orders) != 2 {
			t.Errorf("orders = %v, want 2", got.Orders)
		}
	})

	t.Run("First still reports a missing model", func(t *testing.T) {
		t.Parallel()

		_, err := s.customers.With(customerOrders).Where("name", "nobody").First(ctx)
		if !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("First() error = %v, want ErrModelNotFound", err)
		}
	})

	t.Run("CursorPaginate", func(t *testing.T) {
		t.Parallel()

		page, err := s.customers.With(customerOrders).CursorPaginate(ctx, odm.CursorPagination{Limit: 5})
		if err != nil {
			t.Fatalf("CursorPaginate() error = %v", err)
		}
		if len(page.Data) != 1 || len(page.Data[0].Orders) != 2 {
			t.Errorf("page = %+v, want one customer with two orders", page.Data)
		}
	})
}

func TestRelationFieldsAreNotStored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newShop(t)

	nana := &customer{Name: "nana"}
	create(t, s.customers, nana)
	create(t, s.purchases, &purchase{CustomerID: nana.ID, Total: 10})

	loaded, err := s.customers.With(customerOrders).First(ctx)
	if err != nil {
		t.Fatalf("First() error = %v", err)
	}
	if len(loaded.Orders) != 1 {
		t.Fatalf("orders = %v, want 1", loaded.Orders)
	}

	// Writing a model that carries a loaded relation must not push it into
	// the document — that is what bson:"-" is for.
	if _, err := s.customers.Where("_id", nana.ID).Set("name", "nana kwesi").Update(ctx); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	reloaded := customer{Name: "fresh"}
	reloaded.Orders = loaded.Orders
	if err := s.customers.Create(ctx, &reloaded); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var stored bson.M
	if err := s.customers.Raw().FindOne(ctx, bson.M{"_id": reloaded.ID}).Decode(&stored); err != nil {
		t.Fatalf("FindOne() error = %v", err)
	}
	if _, ok := stored["Orders"]; ok {
		t.Error("the stored document carries an Orders field, want the relation left out")
	}
	if len(stored) != 4 {
		t.Errorf("stored document = %v, want only _id, name and the timestamps", stored)
	}
}
