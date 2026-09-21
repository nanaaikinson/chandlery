package odm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// testNow is the instant every unit test's clock reports, so a compiled
// timestamp is something a test can assert on exactly.
var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// testCollection is a Collection with no Mongo behind it. Every builder
// method, and every terminal method's early error return, works without
// touching the server — which is the whole point of testing query state
// separately from the integration suite.
func testCollection() *Collection[User] {
	return &Collection[User]{meta: metaFor[User](), now: func() time.Time { return testNow }}
}

func TestWhere(t *testing.T) {
	t.Parallel()

	t.Run("builds an equality filter", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("email", "nana@example.com").filter()
		want := bson.M{"email": "nana@example.com"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("ANDs multiple conditions", func(t *testing.T) {
		t.Parallel()

		got := testCollection().
			Where("email", "nana@example.com").
			Where("is_active", true).
			filter()
		want := bson.M{"$and": bson.A{
			bson.M{"email": "nana@example.com"},
			bson.M{"is_active": true},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("keeps both conditions on the same field", func(t *testing.T) {
		t.Parallel()

		// A single merged bson.M could only hold one of these.
		got := testCollection().Where("age", 18).Where("age", 21).filter()
		want := bson.M{"$and": bson.A{
			bson.M{"age": 18},
			bson.M{"age": 21},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("matches everything when unfiltered", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Query().filter()
		if !reflect.DeepEqual(got, bson.M{}) {
			t.Errorf("filter() = %v, want an empty filter", got)
		}
	})
}

func TestQueryImmutability(t *testing.T) {
	t.Parallel()

	t.Run("branches keep independent filters", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("active", true)
		admins := base.Where("role", "admin")
		members := base.Where("role", "member")

		if got, want := base.filter(), (bson.M{"active": true}); !reflect.DeepEqual(got, want) {
			t.Errorf("base filter() = %v, want %v", got, want)
		}
		wantAdmins := bson.M{"$and": bson.A{bson.M{"active": true}, bson.M{"role": "admin"}}}
		if got := admins.filter(); !reflect.DeepEqual(got, wantAdmins) {
			t.Errorf("admins filter() = %v, want %v", got, wantAdmins)
		}
		wantMembers := bson.M{"$and": bson.A{bson.M{"active": true}, bson.M{"role": "member"}}}
		if got := members.filter(); !reflect.DeepEqual(got, wantMembers) {
			t.Errorf("members filter() = %v, want %v", got, wantMembers)
		}
	})

	t.Run("branches keep independent sorts", func(t *testing.T) {
		t.Parallel()

		base := testCollection().OrderBy("created_at", Desc)
		byName := base.OrderBy("name", Asc)
		byEmail := base.OrderBy("email", Desc)

		assertSort(t, "base", base, bson.D{{Key: "created_at", Value: -1}})
		assertSort(t, "byName", byName, bson.D{
			{Key: "created_at", Value: -1},
			{Key: "name", Value: 1},
		})
		assertSort(t, "byEmail", byEmail, bson.D{
			{Key: "created_at", Value: -1},
			{Key: "email", Value: -1},
		})
	})

	t.Run("branches keep independent limits and skips", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("active", true)
		page := base.Limit(20).Skip(40)

		if base.hasLimit || base.hasSkip {
			t.Errorf("base picked up limit/skip from a branch: hasLimit = %t, hasSkip = %t", base.hasLimit, base.hasSkip)
		}
		if page.limit != 20 || page.skip != 40 {
			t.Errorf("page limit/skip = %d/%d, want 20/40", page.limit, page.skip)
		}
	})

	t.Run("an invalid call on a branch leaves the base usable", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("active", true)
		broken := base.Limit(-1)

		if base.err != nil {
			t.Errorf("base err = %v, want nil", base.err)
		}
		if broken.err == nil {
			t.Error("broken err = nil, want an error")
		}
	})
}

func TestWhereIn(t *testing.T) {
	t.Parallel()

	t.Run("builds an $in filter", func(t *testing.T) {
		t.Parallel()

		values := []string{"active", "pending"}
		got := testCollection().WhereIn("status", values).filter()
		want := bson.M{"status": bson.M{"$in": bson.A{"active", "pending"}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("normalizes a typed nil slice to an empty BSON array", func(t *testing.T) {
		t.Parallel()

		var values []string
		got := testCollection().WhereIn("status", values).filter()
		want := bson.M{"status": bson.M{"$in": bson.A{}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("normalizes bytes instead of letting BSON encode binary", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereIn("age", []byte{18, 21}).filter()
		want := bson.M{"age": bson.M{"$in": bson.A{byte(18), byte(21)}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("rejects a non-slice", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().WhereIn("status", "active"))
	})

	t.Run("rejects nil values", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().WhereIn("status", nil))
	})
}

func TestWhereRaw(t *testing.T) {
	t.Parallel()

	t.Run("ANDs a raw filter with a regular condition", func(t *testing.T) {
		t.Parallel()

		or := bson.M{"$or": bson.A{
			bson.M{"email": "nana@example.com"},
			bson.M{"phone": "+233000000000"},
		}}
		got := testCollection().Where("is_active", true).WhereRaw(or).filter()
		want := bson.M{"$and": bson.A{bson.M{"is_active": true}, or}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("passes a lone raw filter through unwrapped", func(t *testing.T) {
		t.Parallel()

		or := bson.M{"$or": bson.A{bson.M{"a": 1}, bson.M{"b": 2}}}
		if got := testCollection().WhereRaw(or).filter(); !reflect.DeepEqual(got, or) {
			t.Errorf("filter() = %v, want %v", got, or)
		}
	})

	t.Run("accepts bson.D", func(t *testing.T) {
		t.Parallel()

		raw := bson.D{{Key: "status", Value: "active"}}
		got := testCollection().Where("is_active", true).WhereRaw(raw).filter()
		want := bson.M{"$and": bson.A{bson.M{"is_active": true}, raw}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("rejects a nil filter", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().WhereRaw(nil))
	})
}

func TestOrderBy(t *testing.T) {
	t.Parallel()

	t.Run("keeps multiple sorts in call order", func(t *testing.T) {
		t.Parallel()

		query := testCollection().
			OrderBy("created_at", Desc).
			OrderBy("name", Asc)
		assertSort(t, "query", query, bson.D{
			{Key: "created_at", Value: -1},
			{Key: "name", Value: 1},
		})
	})

	t.Run("rejects an unknown direction", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().OrderBy("created_at", Direction(7)))
	})
}

func TestLimitAndSkip(t *testing.T) {
	t.Parallel()

	t.Run("records a limit", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Limit(20)
		if !query.hasLimit || query.limit != 20 {
			t.Errorf("hasLimit/limit = %t/%d, want true/20", query.hasLimit, query.limit)
		}
	})

	t.Run("records a skip", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Skip(40)
		if !query.hasSkip || query.skip != 40 {
			t.Errorf("hasSkip/skip = %t/%d, want true/40", query.hasSkip, query.skip)
		}
	})

	t.Run("accepts a zero limit as MongoDB's own no-limit", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Limit(0)
		if query.err != nil {
			t.Errorf("err = %v, want nil", query.err)
		}
		if !query.hasLimit || query.limit != 0 {
			t.Errorf("hasLimit/limit = %t/%d, want true/0", query.hasLimit, query.limit)
		}
	})

	t.Run("rejects a negative limit", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Limit(-1))
	})

	t.Run("rejects a negative skip", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Skip(-1))
	})
}

func TestInvalidQueryKeepsTheFirstError(t *testing.T) {
	t.Parallel()

	query := testCollection().Limit(-1).Skip(-2)
	if got, want := query.err.Error(), "Limit(-1)"; !errors.Is(query.err, ErrInvalidQuery) || !strings.Contains(got, want) {
		t.Errorf("err = %v, want the first failure (%s)", query.err, want)
	}
}

// assertInvalidQuery checks that a builder failure reaches the caller
// through every terminal method, rather than only whichever one a test
// happened to call. None of them can reach Mongo here, so an error from any
// other source would fail the test just as loudly.
func assertInvalidQuery(t *testing.T, query *Query[User]) {
	t.Helper()

	ctx := context.Background()

	if _, err := query.Get(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Get() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.First(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("First() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.Find(ctx, "id"); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Find() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.Count(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Count() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.Exists(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Exists() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.Update(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Update() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.UpdateOne(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("UpdateOne() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.UpdateRaw(ctx, bson.M{"$set": bson.M{"a": 1}}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("UpdateRaw() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.Delete(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Delete() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.DeleteOne(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("DeleteOne() error = %v, want ErrInvalidQuery", err)
	}
}

func assertSort(t *testing.T, name string, query *Query[User], want bson.D) {
	t.Helper()

	if got := query.sorts; !reflect.DeepEqual(got, want) {
		t.Errorf("%s sorts = %v, want %v", name, got, want)
	}
}

func TestProjection(t *testing.T) {
	t.Parallel()

	t.Run("Select builds an inclusion projection in call order", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Select("name", "email").Select("created_at")
		want := bson.D{
			{Key: "name", Value: 1},
			{Key: "email", Value: 1},
			{Key: "created_at", Value: 1},
		}
		assertProjection(t, query, want)
	})

	t.Run("Exclude builds an exclusion projection", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Exclude("password_hash", "internal_notes")
		want := bson.D{
			{Key: "password_hash", Value: 0},
			{Key: "internal_notes", Value: 0},
		}
		assertProjection(t, query, want)
	})

	t.Run("allows _id to be excluded from an inclusion projection", func(t *testing.T) {
		t.Parallel()

		want := bson.D{{Key: "name", Value: 1}, {Key: "_id", Value: 0}}
		assertProjection(t, testCollection().Select("name").Exclude("_id"), want)
	})

	t.Run("allows an inclusion after an _id-only exclusion", func(t *testing.T) {
		t.Parallel()

		want := bson.D{{Key: "_id", Value: 0}, {Key: "name", Value: 1}}
		assertProjection(t, testCollection().Exclude("_id").Select("name"), want)
	})

	t.Run("ignores a repeated identical field", func(t *testing.T) {
		t.Parallel()

		want := bson.D{{Key: "name", Value: 1}}
		assertProjection(t, testCollection().Select("name").Select("name"), want)
	})

	t.Run("rejects excluding another field from an inclusion projection", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Select("name").Exclude("email"))
	})

	t.Run("rejects including a field in an exclusion projection", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Exclude("password_hash").Select("name"))
	})

	t.Run("rejects contradicting itself on one field", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Select("_id").Exclude("_id"))
	})

	t.Run("rejects an empty field list", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Select())
		assertInvalidQuery(t, testCollection().Exclude())
	})

	t.Run("branches keep independent projections", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Select("name")
		withEmail := base.Select("email")
		withPhone := base.Select("phone")

		assertProjection(t, base, bson.D{{Key: "name", Value: 1}})
		assertProjection(t, withEmail, bson.D{{Key: "name", Value: 1}, {Key: "email", Value: 1}})
		assertProjection(t, withPhone, bson.D{{Key: "name", Value: 1}, {Key: "phone", Value: 1}})
	})

	t.Run("an invalid projection on a branch leaves the base usable", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Select("name")
		broken := base.Exclude("email")

		if base.err != nil {
			t.Errorf("base err = %v, want nil", base.err)
		}
		if broken.err == nil {
			t.Error("broken err = nil, want an error")
		}
		assertProjection(t, base, bson.D{{Key: "name", Value: 1}})
	})
}

func TestLatestAndOldest(t *testing.T) {
	t.Parallel()

	t.Run("Latest defaults to created_at descending", func(t *testing.T) {
		t.Parallel()

		assertSort(t, "Latest()", testCollection().Latest(), bson.D{{Key: "created_at", Value: -1}})
	})

	t.Run("Oldest defaults to created_at ascending", func(t *testing.T) {
		t.Parallel()

		assertSort(t, "Oldest()", testCollection().Oldest(), bson.D{{Key: "created_at", Value: 1}})
	})

	t.Run("Latest takes explicit fields, all descending", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Latest("published_at", "_id")
		assertSort(t, "Latest(fields)", query, bson.D{
			{Key: "published_at", Value: -1},
			{Key: "_id", Value: -1},
		})
	})

	t.Run("composes with OrderBy in call order", func(t *testing.T) {
		t.Parallel()

		query := testCollection().OrderBy("score", Asc).Latest()
		assertSort(t, "OrderBy+Latest", query, bson.D{
			{Key: "score", Value: 1},
			{Key: "created_at", Value: -1},
		})
	})
}

func assertProjection(t *testing.T, query *Query[User], want bson.D) {
	t.Helper()

	if query.err != nil {
		t.Fatalf("err = %v, want nil", query.err)
	}
	if got := query.projection; !reflect.DeepEqual(got, want) {
		t.Errorf("projection = %v, want %v", got, want)
	}
}
