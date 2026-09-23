package odm_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/nanaaikinson/chandlery/odm"
)

// User is the model the examples read and write.
type User struct {
	odm.Model       `bson:",inline"`
	odm.SoftDeletes `bson:",inline"`

	BusinessID string `bson:"business_id"`
	Name       string `bson:"name"`
	Email      string `bson:"email"`
	IsActive   bool   `bson:"is_active"`

	// Loaded by With, never stored.
	Orders []Order `bson:"-"`
}

func (User) CollectionName() string { return "users" }

func (User) Indexes() []odm.Index {
	return []odm.Index{
		{Keys: bson.D{{Key: "email", Value: 1}}, Unique: true},
		{Keys: bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}}},
	}
}

// BeforeCreate normalizes the email before the document is written.
func (u *User) BeforeCreate(context.Context) error {
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	return nil
}

// Order belongs to a User.
type Order struct {
	odm.Model `bson:",inline"`

	UserID bson.ObjectID `bson:"user_id"`
	Total  int64         `bson:"total"`

	Payments []Payment `bson:"-"`
}

func (Order) CollectionName() string { return "orders" }

// Payment hangs off an Order, one level further down.
type Payment struct {
	odm.Model `bson:",inline"`

	OrderID bson.ObjectID `bson:"order_id"`
	Amount  int64         `bson:"amount"`
}

func (Payment) CollectionName() string { return "payments" }

// The links are declared once, next to the models.
var (
	UserOrders = odm.HasMany[User, Order]{
		ForeignKey: "user_id",
		Attach:     func(u *User, orders []Order) { u.Orders = orders },
	}
	OrderPayments = odm.HasMany[Order, Payment]{
		ForeignKey: "order_id",
		Attach:     func(o *Order, payments []Payment) { o.Payments = payments },
	}
)

// Active is a scope: a named piece of filtering, reusable anywhere.
func Active(q *odm.Query[User]) *odm.Query[User] {
	return q.Where("is_active", true)
}

func Example() {
	ctx := context.Background()

	// odm.New takes an already-connected database. Opening and closing the
	// client stays yours.
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)

	database := odm.New(client.Database("app"))
	users := odm.Use[User](database)

	user := User{Name: "Nana", Email: "  Nana@Example.com "}
	if err := users.Create(ctx, &user); err != nil {
		log.Fatal(err)
	}
	fmt.Println(!user.ID.IsZero(), user.Email)

	found, err := users.Where("email", "nana@example.com").First(ctx)
	if errors.Is(err, odm.ErrModelNotFound) {
		fmt.Println("no such user")
		return
	} else if err != nil {
		log.Fatal(err)
	}
	fmt.Println(found.Name)
}

func ExampleQuery_Where() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// Equality, comparison, sets, ranges and nulls, all ANDed together.
	adults, err := users.
		Where("business_id", "b1").
		Where("age", odm.Gte, 18).
		WhereNotIn("status", []string{"blocked", "deleted"}).
		WhereNotNull("email").
		OrderBy("created_at", odm.Desc).
		Select("name", "email", "created_at").
		Limit(20).
		Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(adults))
}

func ExampleQuery_OrWhere() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// OrWhere ORs with everything before it: (is_admin OR is_owner).
	admins, err := users.Where("is_admin", true).OrWhere("is_owner", true).Get(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// A grouped predicate needs an explicit $or, which is what WhereRaw is
	// for: is_active AND (email = x OR phone = y).
	contacts, err := users.
		Where("is_active", true).
		WhereRaw(bson.M{"$or": bson.A{
			bson.M{"email": "nana@example.com"},
			bson.M{"phone": "+233000000000"},
		}}).
		Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(admins), len(contacts))
}

func ExampleQuery_Scope() {
	var users *odm.Collection[User]
	ctx := context.Background()

	list, err := users.Scope(Active).Where("business_id", "b1").Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(list))
}

func ExampleQuery_Update() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// Atomic operators, not a whole-document rewrite. updated_at comes
	// along automatically on a model embedding odm.Model.
	result, err := users.
		Where("_id", "01H0").
		Set("name", "Nana Kwesi").
		Inc("login_count", 1).
		Update(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.ModifiedCount)
}

func ExampleCollection_Save() {
	var users *odm.Collection[User]
	ctx := context.Background()

	user, err := users.Find(ctx, "01H0")
	if err != nil {
		log.Fatal(err)
	}

	user.Name = "Nana Kwesi"

	// Writes only what changed — and nothing at all if nothing did.
	if err := users.Save(ctx, &user); err != nil {
		log.Fatal(err)
	}

	changes, err := odm.Changes(&user)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(changes.Set)) // 0: saving left the model clean
}

func ExampleQuery_CursorPaginate() {
	var users *odm.Collection[User]
	ctx := context.Background()

	cursor := "" // from the previous page, or empty for the first
	for {
		page, err := users.
			Where("business_id", "b1").
			OrderBy("created_at", odm.Desc).
			CursorPaginate(ctx, odm.CursorPagination{Limit: 20, Cursor: cursor})
		if errors.Is(err, odm.ErrInvalidCursor) {
			fmt.Println("stale cursor, start again")
			return
		} else if err != nil {
			log.Fatal(err)
		}

		for _, user := range page.Data {
			fmt.Println(user.Name)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
}

func ExampleQuery_With() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// Two queries in total, whatever the number of users: one for them,
	// one for every order belonging to any of them.
	list, err := users.With(UserOrders).Where("business_id", "b1").Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range list {
		fmt.Println(user.Name, len(user.Orders))
	}
}

func ExampleHasMany_With() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// Three levels, three queries: the users, every order belonging to any
	// of them, then every payment belonging to any of those.
	list, err := users.With(UserOrders.With(OrderPayments)).Get(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range list {
		for _, order := range user.Orders {
			fmt.Println(user.Name, order.Total, len(order.Payments))
		}
	}
}

func ExampleQuery_Delete() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// User embeds odm.SoftDeletes, so this stamps deleted_at and later
	// reads skip the document.
	if _, err := users.Where("_id", "01H0").Delete(ctx); err != nil {
		log.Fatal(err)
	}

	trashed, err := users.OnlyTrashed().Count(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if _, err := users.Where("_id", "01H0").Restore(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println(trashed)
}

func ExampleAggregateInto() {
	var users *odm.Collection[User]
	ctx := context.Background()

	type byBusiness struct {
		BusinessID string `bson:"_id"`
		Total      int64  `bson:"total"`
	}

	rows, err := odm.AggregateInto[User, byBusiness](ctx, users.Scope(Active), mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   "$business_id",
			"total": bson.M{"$sum": 1},
		}}},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, row := range rows {
		fmt.Println(row.BusinessID, row.Total)
	}
}

func ExampleDB_Transaction() {
	var database *odm.DB
	var users *odm.Collection[User]
	var orders *odm.Collection[Order]
	ctx := context.Background()

	err := database.Transaction(ctx, func(ctx context.Context) error {
		// This ctx carries the session. Every call inside must use it —
		// one using the outer ctx would commit on its own.
		user := User{Name: "Nana"}
		if err := users.Create(ctx, &user); err != nil {
			return err
		}
		return orders.Create(ctx, &Order{UserID: user.ID, Total: 100})
	})
	if err != nil {
		log.Fatal(err)
	}
}

// WelcomeObserver sends mail after a user is created.
type WelcomeObserver struct{}

func (WelcomeObserver) Created(_ context.Context, user *User) error {
	fmt.Println("welcome", user.Email)
	return nil
}

func ExampleObserve() {
	var database *odm.DB

	// Registered per database — there is no package-global registry.
	odm.Observe[User](database, WelcomeObserver{})
}

func ExampleCollection_SyncIndexes() {
	var users *odm.Collection[User]
	ctx := context.Background()

	// Safe on every start: an index that already exists is left alone, and
	// nothing is ever dropped.
	if err := users.SyncIndexes(ctx); err != nil {
		log.Fatal(err)
	}
}

func ExampleWithClock() {
	var client *mongo.Client

	// A fixed clock makes created_at/updated_at assertable in tests.
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	database := odm.New(client.Database("app"), odm.WithClock(func() time.Time {
		return fixed
	}))
	fmt.Println(database.Raw().Name())
}
