//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/nanaaikinson/chandlery/odm"
)

// user is the suite's model: an embedded odm.Model (so Create's ULID and
// timestamp handling is exercised) plus a few fields to filter and sort on.
type user struct {
	odm.Model `bson:",inline"`

	Name       string   `bson:"name"`
	Email      string   `bson:"email"`
	Status     string   `bson:"status"`
	Age        int      `bson:"age"`
	IsActive   bool     `bson:"is_active"`
	Nickname   string   `bson:"nickname"`
	LoginCount int      `bson:"login_count"`
	Tags       []string `bson:"tags"`
	Roles      []string `bson:"roles"`
}

func (user) CollectionName() string { return "users" }

type objectIDUser struct {
	ID   bson.ObjectID `bson:"_id"`
	Name string        `bson:"name"`
}

func (objectIDUser) CollectionName() string { return "object_id_users" }

// clockAt is a fixed time source, so tests can assert on the exact instant
// Create stamps rather than on "something close to now".
var clockAt = func(at time.Time) odm.Option {
	return odm.WithClock(func() time.Time { return at })
}

// newUsers gives each test its own database, so tests stay isolated while
// running in parallel — the Mongo analogue of db's per-test transaction and
// cache/redis's per-store key prefix. Mongo creates a database on first
// write, so an unused one costs nothing.
func newUsers(t *testing.T, opts ...odm.Option) *odm.Collection[user] {
	t.Helper()

	return odm.Use[user](odm.New(testDatabase(t, client), opts...))
}

// testDatabase gives the calling test its own database, dropped afterwards.
// Handed out raw so a test can wrap it in more than one odm.DB — two clocks
// over one collection, say.
func testDatabase(t *testing.T, on *mongo.Client) *mongo.Database {
	t.Helper()

	database := on.Database(databaseName(t))
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping test database: %v", err)
		}
	})
	return database
}

// databaseName turns a test's name into a legal Mongo database name: the
// characters Mongo rejects (notably the "/" in a subtest name) become
// underscores, and the result is cut to Mongo's 63-byte limit.
func databaseName(t *testing.T) string {
	t.Helper()

	name := "odm_" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, t.Name())

	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

func seed(t *testing.T, users *odm.Collection[user], models ...*user) {
	t.Helper()

	for _, model := range models {
		if err := users.Create(context.Background(), model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
}

func names(models []user) []string {
	out := make([]string, len(models))
	for i, model := range models {
		out[i] = model.Name
	}
	return out
}

func TestCreate(t *testing.T) {
	t.Parallel()

	t.Run("assigns a ULID and timestamps, and round-trips", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		users := newUsers(t, clockAt(at))

		model := user{Name: "Nana", Email: "nana@example.com", IsActive: true}
		if err := users.Create(ctx, &model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		if _, err := ulid.Parse(model.ID); err != nil {
			t.Errorf("Create() left ID = %q, want a ULID: %v", model.ID, err)
		}
		if !model.CreatedAt.Equal(at) || !model.UpdatedAt.Equal(at) {
			t.Errorf("Create() left CreatedAt/UpdatedAt = %v/%v, want both %v", model.CreatedAt, model.UpdatedAt, at)
		}

		stored, err := users.Find(ctx, model.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if stored.Name != model.Name || stored.Email != model.Email || !stored.IsActive {
			t.Errorf("Find() = %+v, want the document just created (%+v)", stored, model)
		}
		if !stored.CreatedAt.Equal(at) {
			t.Errorf("Find() CreatedAt = %v, want %v", stored.CreatedAt, at)
		}
	})

	t.Run("keeps a caller-supplied ID and CreatedAt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		backfilled := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		users := newUsers(t, clockAt(at))

		model := user{Name: "Nana"}
		model.ID = "chosen-id"
		model.CreatedAt = backfilled

		if err := users.Create(ctx, &model); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if model.ID != "chosen-id" {
			t.Errorf("Create() overwrote ID = %q, want %q", model.ID, "chosen-id")
		}
		if !model.CreatedAt.Equal(backfilled) {
			t.Errorf("Create() overwrote CreatedAt = %v, want %v", model.CreatedAt, backfilled)
		}
		if !model.UpdatedAt.Equal(at) {
			t.Errorf("Create() left UpdatedAt = %v, want %v", model.UpdatedAt, at)
		}

		if _, err := users.Find(ctx, "chosen-id"); err != nil {
			t.Errorf("Find() error = %v, want the document stored under its chosen _id", err)
		}
	})
}

func TestGet(t *testing.T) {
	t.Parallel()

	t.Run("returns every document matching ANDed conditions", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "active-admin", Status: "active", IsActive: true},
			&user{Name: "pending", Status: "pending", IsActive: true},
			&user{Name: "inactive", Status: "active", IsActive: false},
		)

		got, err := users.Where("status", "active").Where("is_active", true).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"active-admin"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("returns nothing when no document matches", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "Nana", Status: "active"})

		got, err := users.Where("status", "archived").Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Get() = %v, want no documents", names(got))
		}
	})

	t.Run("applies WhereIn", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "active"},
			&user{Name: "p", Status: "pending"},
			&user{Name: "x", Status: "archived"},
		)

		got, err := users.
			WhereIn("status", []string{"active", "pending"}).
			OrderBy("name", odm.Asc).
			Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"a", "p"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("normalizes bytes into a BSON $in array", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "twenty", Age: 20},
			&user{Name: "thirty", Age: 30},
			&user{Name: "forty", Age: 40},
		)

		got, err := users.WhereIn("age", []byte{20, 30}).OrderBy("age", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"twenty", "thirty"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("normalizes a typed nil slice to no matches", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "active", Status: "active"})
		var statuses []string

		got, err := users.WhereIn("status", statuses).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Get() = %v, want no documents", names(got))
		}
	})

	t.Run("requires both a Where and a WhereRaw to match", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "match", Email: "nana@example.com", IsActive: true},
			&user{Name: "right-email-but-inactive", Email: "nana@example.com"},
			&user{Name: "active-but-other-contact", Email: "other@example.com", IsActive: true},
		)

		got, err := users.
			Where("is_active", true).
			WhereRaw(bson.M{"$or": bson.A{
				bson.M{"email": "nana@example.com"},
				bson.M{"phone": "+233000000000"},
			}}).
			Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"match"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("accepts bson.D raw filters", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "match", Status: "active", IsActive: true},
			&user{Name: "inactive", Status: "active"},
		)

		got, err := users.Where("is_active", true).WhereRaw(bson.D{{Key: "status", Value: "active"}}).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"match"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})
}

func TestCreateWithCustomObjectID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := client.Database(databaseName(t))
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping test database: %v", err)
		}
	})
	users := odm.Use[objectIDUser](odm.New(database))
	want := objectIDUser{ID: bson.NewObjectID(), Name: "Nana"}

	if err := users.Create(ctx, &want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := users.Find(ctx, want.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if got != want {
		t.Errorf("Find() = %+v, want %+v", got, want)
	}
}

func TestFirst(t *testing.T) {
	t.Parallel()

	t.Run("returns the first document in sort order", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "b", Status: "active", Age: 30},
			&user{Name: "a", Status: "active", Age: 40},
			&user{Name: "c", Status: "archived", Age: 50},
		)

		got, err := users.Where("status", "active").OrderBy("age", odm.Desc).First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "a" {
			t.Errorf("First() = %q, want %q", got.Name, "a")
		}
	})

	t.Run("reports ErrModelNotFound when nothing matches", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "Nana", Status: "active"})

		got, err := users.Where("status", "archived").First(ctx)
		if !errors.Is(err, odm.ErrModelNotFound) {
			t.Fatalf("First() error = %v, want ErrModelNotFound", err)
		}
		// The driver's own error stays reachable underneath.
		if !errors.Is(err, mongo.ErrNoDocuments) {
			t.Errorf("First() error = %v, want it to wrap mongo.ErrNoDocuments", err)
		}
		if got.Name != "" {
			t.Errorf("First() = %+v, want the zero model", got)
		}
	})
}

func TestFind(t *testing.T) {
	t.Parallel()

	t.Run("returns the document with that _id", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		wanted := &user{Name: "Nana"}
		seed(t, users, wanted, &user{Name: "Other"})

		got, err := users.Find(ctx, wanted.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.Name != "Nana" {
			t.Errorf("Find() = %q, want %q", got.Name, "Nana")
		}
	})

	t.Run("reports ErrModelNotFound for an unknown id", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "Nana"})

		if _, err := users.Find(ctx, "no-such-id"); !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("Find() error = %v, want ErrModelNotFound", err)
		}
	})

	t.Run("still honours conditions already on the query", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		archived := &user{Name: "Nana", Status: "archived"}
		seed(t, users, archived)

		if _, err := users.Where("status", "active").Find(ctx, archived.ID); !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("Find() error = %v, want ErrModelNotFound", err)
		}
	})
}

func TestCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "a", Status: "active"},
		&user{Name: "b", Status: "active"},
		&user{Name: "c", Status: "archived"},
	)

	t.Run("counts the documents matching the filter", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("status", "active").Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if got != 2 {
			t.Errorf("Count() = %d, want 2", got)
		}
	})

	t.Run("ignores Limit and Skip, reporting the full total", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("status", "active").Limit(1).Skip(1).Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if got != 2 {
			t.Errorf("Count() = %d, want 2", got)
		}
	})

	t.Run("counts the whole collection when unfiltered", func(t *testing.T) {
		t.Parallel()

		got, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if got != 3 {
			t.Errorf("Count() = %d, want 3", got)
		}
	})
}

func TestExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users, &user{Name: "Nana", Email: "nana@example.com"})

	t.Run("true when a document matches", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("email", "nana@example.com").Exists(ctx)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if !got {
			t.Error("Exists() = false, want true")
		}
	})

	t.Run("false when nothing matches", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("email", "someone@example.com").Exists(ctx)
		if err != nil {
			t.Fatalf("Exists() error = %v", err)
		}
		if got {
			t.Error("Exists() = true, want false")
		}
	})
}

func TestOrderLimitSkip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "b", Status: "active", Age: 30},
		&user{Name: "a", Status: "active", Age: 30},
		&user{Name: "c", Status: "active", Age: 20},
		&user{Name: "d", Status: "active", Age: 40},
	)

	t.Run("sorts descending", func(t *testing.T) {
		t.Parallel()

		got, err := users.OrderBy("age", odm.Desc).OrderBy("name", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"d", "a", "b", "c"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("later OrderBy calls break the earlier one's ties", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("age", 30).OrderBy("age", odm.Asc).OrderBy("name", odm.Desc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"b", "a"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("limits and skips as a page", func(t *testing.T) {
		t.Parallel()

		got, err := users.OrderBy("name", odm.Asc).Limit(2).Skip(1).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"b", "c"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("First honours Skip", func(t *testing.T) {
		t.Parallel()

		got, err := users.OrderBy("name", odm.Asc).Skip(2).First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "c" {
			t.Errorf("First() = %q, want %q", got.Name, "c")
		}
	})
}

func TestRawAccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := client.Database(databaseName(t))
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("dropping test database: %v", err)
		}
	})

	db := odm.New(database)
	users := odm.Use[user](db)
	seed(t, users, &user{Name: "Nana"})

	if got := db.Raw().Name(); got != database.Name() {
		t.Errorf("DB.Raw().Name() = %q, want %q", got, database.Name())
	}
	if got := users.Raw().Name(); got != "users" {
		t.Errorf("Collection.Raw().Name() = %q, want %q", got, "users")
	}

	// The native handle reaches the same documents the builder writes.
	count, err := users.Raw().CountDocuments(ctx, bson.M{"name": "Nana"})
	if err != nil {
		t.Fatalf("CountDocuments() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountDocuments() = %d, want 1", count)
	}
}
