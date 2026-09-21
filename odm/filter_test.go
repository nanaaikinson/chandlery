package odm

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestWhereComparisonOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		operator string
		want     any
	}{
		{"=", 18},
		{"==", 18},
		{"!=", bson.M{"$ne": 18}},
		{">", bson.M{"$gt": 18}},
		{">=", bson.M{"$gte": 18}},
		{"<", bson.M{"$lt": 18}},
		{"<=", bson.M{"$lte": 18}},
	}

	for _, test := range tests {
		t.Run(test.operator, func(t *testing.T) {
			t.Parallel()

			got := testCollection().Where("age", test.operator, 18).filter()
			want := bson.M{"age": test.want}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("filter() = %v, want %v", got, want)
			}
		})
	}
}

func TestWhereTypedOperators(t *testing.T) {
	t.Parallel()

	// The typed constants and their string spellings have to compile to the
	// same filter, or one of the two forms is a trap.
	pairs := []struct {
		typed Operator
		spelt string
	}{
		{Eq, "="},
		{Ne, "!="},
		{Gt, ">"},
		{Gte, ">="},
		{Lt, "<"},
		{Lte, "<="},
	}

	for _, pair := range pairs {
		t.Run(string(pair.typed), func(t *testing.T) {
			t.Parallel()

			typed := testCollection().Where("age", pair.typed, 18).filter()
			spelt := testCollection().Where("age", pair.spelt, 18).filter()
			if !reflect.DeepEqual(typed, spelt) {
				t.Errorf("Where(odm.%v) = %v, Where(%q) = %v, want the same filter", pair.typed, typed, pair.spelt, spelt)
			}
		})
	}

	t.Run("rejects an Operator that is not one of the constants", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("age", Operator("=>"), 18))
	})

	t.Run("composes like any other condition", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("age", Gte, 18).Where("age", Lte, 65).filter()
		want := any(bson.M{"$and": bson.A{
			bson.M{"age": bson.M{"$gte": 18}},
			bson.M{"age": bson.M{"$lte": 65}},
		}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})
}

func TestWhereArgumentValidation(t *testing.T) {
	t.Parallel()

	t.Run("rejects an unknown operator", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("age", "=>", 18))
	})

	t.Run("rejects a LIKE-style operator rather than reinterpreting it", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("name", "like", "nana%"))
	})

	t.Run("rejects an operator that is neither a string nor an Operator", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("age", 18, 21))
	})

	t.Run("rejects no value at all", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("age"))
	})

	t.Run("rejects more arguments than an operator form takes", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("age", ">", 18, 21))
	})

	t.Run("treats an operator-looking value as a value in the two-argument form", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("symbol", ">=").filter()
		want := bson.M{"symbol": ">="}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})
}

func TestWhereRangeOnOneField(t *testing.T) {
	t.Parallel()

	// The regression this package's fragment list exists for: merging into
	// one bson.M would keep only the second bound.
	got := testCollection().
		Where("age", ">=", 18).
		Where("age", "<=", 65).
		filter()
	want := bson.M{"$and": bson.A{
		bson.M{"age": bson.M{"$gte": 18}},
		bson.M{"age": bson.M{"$lte": 65}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filter() = %v, want %v", got, want)
	}
}

func TestOrWhere(t *testing.T) {
	t.Parallel()

	t.Run("ORs two conditions", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("is_admin", true).OrWhere("is_owner", true).filter()
		want := bson.M{"$or": bson.A{
			bson.M{"is_admin": true},
			bson.M{"is_owner": true},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("ORs against everything accumulated so far", func(t *testing.T) {
		t.Parallel()

		// (active AND age >= 18) OR role == admin
		got := testCollection().
			Where("active", true).
			Where("age", ">=", 18).
			OrWhere("role", "admin").
			filter()
		want := bson.M{"$or": bson.A{
			bson.M{"$and": bson.A{
				bson.M{"active": true},
				bson.M{"age": bson.M{"$gte": 18}},
			}},
			bson.M{"role": "admin"},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("ANDs a later Where against the whole OR group", func(t *testing.T) {
		t.Parallel()

		// (active OR role == admin) AND verified — left to right, not SQL
		// precedence, which would read active OR (role AND verified).
		got := testCollection().
			Where("active", true).
			OrWhere("role", "admin").
			Where("verified", true).
			filter()
		want := bson.M{"$and": bson.A{
			bson.M{"$or": bson.A{
				bson.M{"active": true},
				bson.M{"role": "admin"},
			}},
			bson.M{"verified": true},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("chains left-nested across repeated calls", func(t *testing.T) {
		t.Parallel()

		got := testCollection().
			Where("a", 1).
			OrWhere("b", 2).
			OrWhere("c", 3).
			filter()
		want := bson.M{"$or": bson.A{
			bson.M{"$or": bson.A{bson.M{"a": 1}, bson.M{"b": 2}}},
			bson.M{"c": 3},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("behaves as Where when it opens the chain", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Query().OrWhere("role", "admin").filter()
		want := bson.M{"role": "admin"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("ORs a raw filter's conditions too", func(t *testing.T) {
		t.Parallel()

		raw := bson.M{"$or": bson.A{bson.M{"email": "a"}, bson.M{"phone": "b"}}}
		got := testCollection().WhereRaw(raw).OrWhere("role", "admin").filter()
		want := bson.M{"$or": bson.A{raw, bson.M{"role": "admin"}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("leaves the query it branched from alone", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("active", true)
		ored := base.OrWhere("role", "admin")

		if got, want := base.filter(), any(bson.M{"active": true}); !reflect.DeepEqual(got, want) {
			t.Errorf("base filter() = %v, want %v", got, want)
		}
		if _, ok := ored.filter().(bson.M)["$or"]; !ok {
			t.Errorf("branch filter() = %v, want an $or", ored.filter())
		}
	})

	t.Run("supports comparison operators", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("age", "<", 18).OrWhere("age", ">", 65).filter()
		want := bson.M{"$or": bson.A{
			bson.M{"age": bson.M{"$lt": 18}},
			bson.M{"age": bson.M{"$gt": 65}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("rejects an unknown operator", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Where("a", 1).OrWhere("age", "=>", 18))
	})
}

func TestWhereNotIn(t *testing.T) {
	t.Parallel()

	t.Run("builds a $nin filter", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereNotIn("status", []string{"blocked", "deleted"}).filter()
		want := bson.M{"status": bson.M{"$nin": bson.A{"blocked", "deleted"}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("passes an empty slice through as MongoDB's match-everything $nin", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereNotIn("status", []string{}).filter()
		want := bson.M{"status": bson.M{"$nin": bson.A{}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("rejects a non-slice", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().WhereNotIn("status", "blocked"))
	})
}

func TestWhereNull(t *testing.T) {
	t.Parallel()

	t.Run("WhereNull matches null or absent", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereNull("deleted_at").filter()
		want := bson.M{"deleted_at": nil}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("WhereNotNull matches present and non-null", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereNotNull("email").filter()
		want := bson.M{"email": bson.M{"$ne": nil}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})
}

func TestWhereBetween(t *testing.T) {
	t.Parallel()

	t.Run("builds an inclusive range", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereBetween("age", 18, 65).filter()
		want := bson.M{"age": bson.M{"$gte": 18, "$lte": 65}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("WhereNotBetween builds an $or of the two open sides", func(t *testing.T) {
		t.Parallel()

		got := testCollection().WhereNotBetween("age", 18, 65).filter()
		want := bson.M{"$or": bson.A{
			bson.M{"age": bson.M{"$lt": 18}},
			bson.M{"age": bson.M{"$gt": 65}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("composes with other conditions under AND", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("active", true).WhereBetween("age", 18, 65).filter()
		want := bson.M{"$and": bson.A{
			bson.M{"active": true},
			bson.M{"age": bson.M{"$gte": 18, "$lte": 65}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})
}
