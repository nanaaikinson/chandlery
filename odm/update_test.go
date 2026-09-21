package odm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// compiled returns the update document a query would send.
func compiled(query *Query[User]) bson.D {
	return compileUpdate(query.updates)
}

func TestUpdateOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func(*Query[User]) *Query[User]
		want  bson.D
	}{
		{
			name:  "Set",
			build: func(q *Query[User]) *Query[User] { return q.Set("name", "Nana") },
			want:  bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: "Nana"}}}},
		},
		{
			name:  "Unset",
			build: func(q *Query[User]) *Query[User] { return q.Unset("nickname") },
			want:  bson.D{{Key: "$unset", Value: bson.D{{Key: "nickname", Value: ""}}}},
		},
		{
			name:  "Inc",
			build: func(q *Query[User]) *Query[User] { return q.Inc("login_count", 1) },
			want:  bson.D{{Key: "$inc", Value: bson.D{{Key: "login_count", Value: 1}}}},
		},
		{
			name:  "Increment",
			build: func(q *Query[User]) *Query[User] { return q.Increment("login_count", 2) },
			want:  bson.D{{Key: "$inc", Value: bson.D{{Key: "login_count", Value: int64(2)}}}},
		},
		{
			name:  "Decrement negates",
			build: func(q *Query[User]) *Query[User] { return q.Decrement("credits", 3) },
			want:  bson.D{{Key: "$inc", Value: bson.D{{Key: "credits", Value: int64(-3)}}}},
		},
		{
			name:  "Push",
			build: func(q *Query[User]) *Query[User] { return q.Push("tags", "go") },
			want:  bson.D{{Key: "$push", Value: bson.D{{Key: "tags", Value: "go"}}}},
		},
		{
			name:  "Pull",
			build: func(q *Query[User]) *Query[User] { return q.Pull("tags", "php") },
			want:  bson.D{{Key: "$pull", Value: bson.D{{Key: "tags", Value: "php"}}}},
		},
		{
			name:  "AddToSet",
			build: func(q *Query[User]) *Query[User] { return q.AddToSet("roles", "admin") },
			want:  bson.D{{Key: "$addToSet", Value: bson.D{{Key: "roles", Value: "admin"}}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := compiled(test.build(testCollection().Query())); !reflect.DeepEqual(got, test.want) {
				t.Errorf("update = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUpdateComposition(t *testing.T) {
	t.Parallel()

	t.Run("groups repeated calls under one operator, in call order", func(t *testing.T) {
		t.Parallel()

		got := compiled(testCollection().Query().
			Set("name", "Nana").
			Set("email", "nana@example.com"))
		want := bson.D{{Key: "$set", Value: bson.D{
			{Key: "name", Value: "Nana"},
			{Key: "email", Value: "nana@example.com"},
		}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("keeps operators in first-use order and regroups later calls", func(t *testing.T) {
		t.Parallel()

		got := compiled(testCollection().Query().
			Set("name", "Nana").
			Inc("login_count", 1).
			Set("email", "nana@example.com").
			Unset("nickname"))
		want := bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "name", Value: "Nana"},
				{Key: "email", Value: "nana@example.com"},
			}},
			{Key: "$inc", Value: bson.D{{Key: "login_count", Value: 1}}},
			{Key: "$unset", Value: bson.D{{Key: "nickname", Value: ""}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("compiles nothing for a query with no staged operators", func(t *testing.T) {
		t.Parallel()

		if got := compiled(testCollection().Where("active", true)); len(got) != 0 {
			t.Errorf("update = %v, want empty", got)
		}
	})
}

func TestUpdateFieldConflicts(t *testing.T) {
	t.Parallel()

	// MongoDB rejects an update that targets one path twice, whichever
	// operators are involved, so the builder refuses to assemble one.
	conflicts := map[string]*Query[User]{
		"same operator twice":      testCollection().Query().Set("name", "a").Set("name", "b"),
		"two operators one field":  testCollection().Query().Set("count", 1).Inc("count", 1),
		"repeated push":            testCollection().Query().Push("tags", "a").Push("tags", "b"),
		"set then unset one field": testCollection().Query().Set("nickname", "x").Unset("nickname"),
	}

	for name, query := range conflicts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertInvalidQuery(t, query)
		})
	}

	t.Run("different fields are fine", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Query().Set("a", 1).Set("b", 2).Inc("c", 1)
		if query.err != nil {
			t.Errorf("err = %v, want nil", query.err)
		}
	})
}

func TestUpdateImmutability(t *testing.T) {
	t.Parallel()

	base := testCollection().Where("_id", "id")
	rename := base.Set("name", "Nana")
	login := base.Inc("login_count", 1)

	if got := compiled(base); len(got) != 0 {
		t.Errorf("base update = %v, want empty", got)
	}

	wantRename := bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: "Nana"}}}}
	if got := compiled(rename); !reflect.DeepEqual(got, wantRename) {
		t.Errorf("rename update = %v, want %v", got, wantRename)
	}

	wantLogin := bson.D{{Key: "$inc", Value: bson.D{{Key: "login_count", Value: 1}}}}
	if got := compiled(login); !reflect.DeepEqual(got, wantLogin) {
		t.Errorf("login update = %v, want %v", got, wantLogin)
	}

	// Compiling one branch must not leave the other's grouping behind
	// either — compileUpdate builds a fresh document each time.
	if got := compiled(rename); !reflect.DeepEqual(got, wantRename) {
		t.Errorf("rename update on recompile = %v, want %v", got, wantRename)
	}
}

func TestUpdateRequiresOperators(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	query := testCollection().Where("active", true)

	if _, err := query.Update(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("Update() error = %v, want ErrInvalidQuery", err)
	}
	if _, err := query.UpdateOne(ctx); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("UpdateOne() error = %v, want ErrInvalidQuery", err)
	}
}

func TestUpdateRawValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("rejects a nil update", func(t *testing.T) {
		t.Parallel()

		if _, err := testCollection().Where("a", 1).UpdateRaw(ctx, nil); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("UpdateRaw() error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("rejects a typed nil update", func(t *testing.T) {
		t.Parallel()

		var update bson.M
		if _, err := testCollection().Where("a", 1).UpdateRaw(ctx, update); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("UpdateRaw() error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("refuses to run alongside staged operators", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Where("a", 1).Set("name", "Nana")
		if _, err := query.UpdateRaw(ctx, bson.M{"$set": bson.M{"name": "Other"}}); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("UpdateRaw() error = %v, want ErrInvalidQuery", err)
		}
	})
}
