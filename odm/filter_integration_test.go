//go:build integration

package odm_test

import (
	"context"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

func TestComparisonQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "minor", Age: 15},
		&user{Name: "eighteen", Age: 18},
		&user{Name: "adult", Age: 40},
		&user{Name: "senior", Age: 70},
	)

	tests := []struct {
		name  string
		build func() *odm.Query[user]
		want  []string
	}{
		{
			name:  "greater than",
			build: func() *odm.Query[user] { return users.Where("age", ">", 18) },
			want:  []string{"adult", "senior"},
		},
		{
			name:  "greater or equal",
			build: func() *odm.Query[user] { return users.Where("age", ">=", 18) },
			want:  []string{"eighteen", "adult", "senior"},
		},
		{
			name:  "less than",
			build: func() *odm.Query[user] { return users.Where("age", "<", 18) },
			want:  []string{"minor"},
		},
		{
			name:  "not equal",
			build: func() *odm.Query[user] { return users.Where("age", "!=", 18) },
			want:  []string{"minor", "adult", "senior"},
		},
		{
			name:  "explicit equality",
			build: func() *odm.Query[user] { return users.Where("age", "=", 18) },
			want:  []string{"eighteen"},
		},
		{
			name: "both bounds on one field",
			build: func() *odm.Query[user] {
				return users.Where("age", ">=", 18).Where("age", "<=", 65)
			},
			want: []string{"eighteen", "adult"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.build().OrderBy("age", odm.Asc).Get(ctx)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if !reflect.DeepEqual(names(got), test.want) {
				t.Errorf("Get() = %v, want %v", names(got), test.want)
			}
		})
	}
}

func TestOrWhereQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "admin", Status: "admin", IsActive: false},
		&user{Name: "active-member", Status: "member", IsActive: true},
		&user{Name: "inactive-member", Status: "member", IsActive: false},
	)

	t.Run("matches either condition", func(t *testing.T) {
		t.Parallel()

		got, err := users.Where("is_active", true).OrWhere("status", "admin").OrderBy("name", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"active-member", "admin"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("a later Where narrows the whole OR group", func(t *testing.T) {
		t.Parallel()

		// (is_active OR status == admin) AND status == member
		got, err := users.
			Where("is_active", true).
			OrWhere("status", "admin").
			Where("status", "member").
			Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"active-member"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})
}

func TestWhereNotInQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "a", Status: "active"},
		&user{Name: "b", Status: "blocked"},
		&user{Name: "d", Status: "deleted"},
	)

	t.Run("excludes the listed values", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereNotIn("status", []string{"blocked", "deleted"}).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"a"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("an empty list excludes nothing, as MongoDB defines it", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereNotIn("status", []string{}).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 3 {
			t.Errorf("Get() returned %d documents, want all 3 — $nin: [] matches everything", len(got))
		}
	})

	t.Run("an empty WhereIn matches nothing, as MongoDB defines it", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereIn("status", []string{}).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Get() returned %d documents, want none — $in: [] matches nothing", len(got))
		}
	})
}

func TestNullQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)

	// Inserted raw, because the point is the difference between a field
	// that is explicitly null and one that isn't there at all — which a Go
	// struct can't express, since it always marshals every field.
	documents := []any{
		bson.M{"_id": bson.NewObjectID(), "name": "explicit-null", "deleted_at": nil},
		bson.M{"_id": bson.NewObjectID(), "name": "missing"},
		bson.M{"_id": bson.NewObjectID(), "name": "present", "deleted_at": "2026-01-01"},
	}
	if _, err := users.Raw().InsertMany(ctx, documents); err != nil {
		t.Fatalf("InsertMany() error = %v", err)
	}

	t.Run("WhereNull matches both explicit null and a missing field", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereNull("deleted_at").OrderBy("name", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"explicit-null", "missing"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("WhereNotNull matches only a present, non-null field", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereNotNull("deleted_at").Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"present"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("the documented $type escape hatch narrows to explicit null", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereRaw(bson.M{"deleted_at": bson.M{"$type": "null"}}).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"explicit-null"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})
}

func TestBetweenQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users,
		&user{Name: "young", Age: 17},
		&user{Name: "lower-bound", Age: 18},
		&user{Name: "middle", Age: 40},
		&user{Name: "upper-bound", Age: 65},
		&user{Name: "old", Age: 66},
	)
	// A document with no age at all, to pin down what the $or form does
	// with a missing field.
	if _, err := users.Raw().InsertOne(ctx, bson.M{"_id": bson.NewObjectID(), "name": "ageless"}); err != nil {
		t.Fatalf("InsertOne() error = %v", err)
	}

	t.Run("WhereBetween includes both bounds", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereBetween("age", 18, 65).OrderBy("age", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"lower-bound", "middle", "upper-bound"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})

	t.Run("WhereNotBetween takes the outside and skips a missing field", func(t *testing.T) {
		t.Parallel()

		got, err := users.WhereNotBetween("age", 18, 65).OrderBy("age", odm.Asc).Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		// "ageless" is absent on purpose: the $or of $lt/$gt needs a field
		// to compare, where a $not around the range would have matched it.
		if want := []string{"young", "old"}; !reflect.DeepEqual(names(got), want) {
			t.Errorf("Get() = %v, want %v", names(got), want)
		}
	})
}

func TestProjectionQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	seed(t, users, &user{Name: "Nana", Email: "nana@example.com", Status: "active", Age: 30})

	t.Run("Select returns only the named fields", func(t *testing.T) {
		t.Parallel()

		got, err := users.Select("name", "email").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "Nana" || got.Email != "nana@example.com" {
			t.Errorf("First() = %+v, want name and email populated", got)
		}
		if got.Status != "" || got.Age != 0 {
			t.Errorf("First() = %+v, want unprojected fields left zero", got)
		}
		if got.ID.IsZero() {
			t.Error("First() dropped _id, want it kept unless excluded")
		}
	})

	t.Run("Exclude drops the named fields and keeps the rest", func(t *testing.T) {
		t.Parallel()

		got, err := users.Exclude("email", "status").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "Nana" || got.Age != 30 {
			t.Errorf("First() = %+v, want unexcluded fields populated", got)
		}
		if got.Email != "" || got.Status != "" {
			t.Errorf("First() = %+v, want excluded fields left zero", got)
		}
	})

	t.Run("_id can be excluded alongside an inclusion projection", func(t *testing.T) {
		t.Parallel()

		got, err := users.Select("name").Exclude("_id").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if got.Name != "Nana" {
			t.Errorf("First() name = %q, want %q", got.Name, "Nana")
		}
		if !got.ID.IsZero() {
			t.Errorf("First() ID = %v, want it excluded", got.ID)
		}
	})

	t.Run("applies to Get as well", func(t *testing.T) {
		t.Parallel()

		got, err := users.Select("name").Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(got) != 1 || got[0].Email != "" {
			t.Errorf("Get() = %+v, want one document with email left zero", got)
		}
	})
}
