package odm

import (
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// order is a related model for the relation unit tests.
type order struct {
	Model `bson:",inline"`

	UserID string `bson:"user_id"`
	Total  int    `bson:"total"`
}

func (order) CollectionName() string { return "orders" }

func rawDocs(t *testing.T, documents ...bson.M) []bson.Raw {
	t.Helper()

	raws := make([]bson.Raw, len(documents))
	for i, document := range documents {
		raw, err := bson.Marshal(document)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		raws[i] = raw
	}
	return raws
}

func TestDocumentKeys(t *testing.T) {
	t.Parallel()

	t.Run("reads one key per document and distinct values to match", func(t *testing.T) {
		t.Parallel()

		raws := rawDocs(t,
			bson.M{"_id": "a"},
			bson.M{"_id": "b"},
			bson.M{"_id": "a"},
		)

		keys, values, err := documentKeys(raws, "_id")
		if err != nil {
			t.Fatalf("documentKeys() error = %v", err)
		}
		if len(keys) != 3 {
			t.Fatalf("keys = %d, want one per document", len(keys))
		}
		if keys[0] != keys[2] || keys[0] == keys[1] {
			t.Error("keys do not identify equal and differing values")
		}
		// The repeated "a" is matched once, not twice.
		if want := (bson.A{"a", "b"}); !reflect.DeepEqual(values, want) {
			t.Errorf("values = %v, want %v — the $in should not repeat a key", values, want)
		}
	})

	t.Run("leaves a document without the field unkeyed", func(t *testing.T) {
		t.Parallel()

		raws := rawDocs(t, bson.M{"_id": "a"}, bson.M{"name": "no key here"})

		keys, values, err := documentKeys(raws, "_id")
		if err != nil {
			t.Fatalf("documentKeys() error = %v", err)
		}
		if keys[1] != "" {
			t.Errorf("keys[1] = %q, want the empty key for a missing field", keys[1])
		}
		if len(values) != 1 {
			t.Errorf("values = %v, want only the one present key", values)
		}
	})

	t.Run("reads a dotted path into an embedded document", func(t *testing.T) {
		t.Parallel()

		raws := rawDocs(t, bson.M{"profile": bson.M{"company_id": "c1"}})

		_, values, err := documentKeys(raws, "profile.company_id")
		if err != nil {
			t.Fatalf("documentKeys() error = %v", err)
		}
		if want := (bson.A{"c1"}); !reflect.DeepEqual(values, want) {
			t.Errorf("values = %v, want %v", values, want)
		}
	})

	t.Run("reports no keys at all for an unrelated field", func(t *testing.T) {
		t.Parallel()

		raws := rawDocs(t, bson.M{"_id": "a"})

		_, values, err := documentKeys(raws, "nope")
		if err != nil {
			t.Fatalf("documentKeys() error = %v", err)
		}
		if len(values) != 0 {
			t.Errorf("values = %v, want none", values)
		}
	})
}

func TestKeyOf(t *testing.T) {
	t.Parallel()

	same := rawDocs(t, bson.M{"k": "abc"}, bson.M{"k": "abc"})
	if keyOf(same[0].Lookup("k")) != keyOf(same[1].Lookup("k")) {
		t.Error("equal values produced different keys")
	}

	// A string "1" and a number 1 are different keys, as they are different
	// documents to MongoDB.
	mixed := rawDocs(t, bson.M{"k": "1"}, bson.M{"k": 1})
	if keyOf(mixed[0].Lookup("k")) == keyOf(mixed[1].Lookup("k")) {
		t.Error("values of different BSON types produced the same key")
	}
}

func TestGroupByKey(t *testing.T) {
	t.Parallel()

	raws := rawDocs(t,
		bson.M{"user_id": "a", "total": 1},
		bson.M{"user_id": "b", "total": 2},
		bson.M{"user_id": "a", "total": 3},
		bson.M{"total": 4},
	)

	grouped := groupByKey(raws, "user_id")
	if len(grouped) != 2 {
		t.Fatalf("grouped into %d keys, want 2", len(grouped))
	}

	first := keyOf(raws[0].Lookup("user_id"))
	if want := []int{0, 2}; !reflect.DeepEqual(grouped[first], want) {
		t.Errorf("grouped[a] = %v, want %v", grouped[first], want)
	}
	second := keyOf(raws[1].Lookup("user_id"))
	if want := []int{1}; !reflect.DeepEqual(grouped[second], want) {
		t.Errorf("grouped[b] = %v, want %v", grouped[second], want)
	}
}

func TestLocalKeyOr(t *testing.T) {
	t.Parallel()

	if got := localKeyOr(""); got != "_id" {
		t.Errorf("localKeyOr(\"\") = %q, want %q", got, "_id")
	}
	if got := localKeyOr("uuid"); got != "uuid" {
		t.Errorf("localKeyOr(%q) = %q, want it kept", "uuid", got)
	}
}

func TestRelationValidation(t *testing.T) {
	t.Parallel()

	attachMany := func(*User, []order) {}
	attachOne := func(*User, *order) {}

	tests := []struct {
		name     string
		relation Relation[User]
		wantErr  string
	}{
		{
			name:     "HasMany needs a foreign key",
			relation: HasMany[User, order]{Attach: attachMany},
			wantErr:  "ForeignKey",
		},
		{
			name:     "HasMany needs somewhere to attach",
			relation: HasMany[User, order]{ForeignKey: "user_id"},
			wantErr:  "Attach",
		},
		{
			name:     "HasOne needs a foreign key",
			relation: HasOne[User, order]{Attach: attachOne},
			wantErr:  "ForeignKey",
		},
		{
			name:     "BelongsTo needs a foreign key",
			relation: BelongsTo[User, order]{Attach: attachOne},
			wantErr:  "ForeignKey",
		},
		{
			name:     "BelongsTo needs somewhere to attach",
			relation: BelongsTo[User, order]{ForeignKey: "order_id"},
			wantErr:  "Attach",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.relation.validate()
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("validate() = %v, want it to name %s", err, test.wantErr)
			}
		})
	}

	t.Run("a complete declaration validates", func(t *testing.T) {
		t.Parallel()

		relation := HasMany[User, order]{ForeignKey: "user_id", Attach: attachMany}
		if err := relation.validate(); err != nil {
			t.Errorf("validate() = %v, want nil", err)
		}
	})
}

func TestWith(t *testing.T) {
	t.Parallel()

	orders := HasMany[User, order]{
		ForeignKey: "user_id",
		Attach:     func(*User, []order) {},
	}

	t.Run("records the relation", func(t *testing.T) {
		t.Parallel()

		query := testCollection().With(orders)
		if len(query.with) != 1 {
			t.Errorf("with = %d relations, want 1", len(query.with))
		}
	})

	t.Run("branches keep independent relations", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("active", true)
		loaded := base.With(orders)

		if len(base.with) != 0 {
			t.Errorf("base picked up %d relations from a branch", len(base.with))
		}
		if len(loaded.with) != 1 {
			t.Errorf("branch has %d relations, want 1", len(loaded.with))
		}
	})

	t.Run("accumulates across calls", func(t *testing.T) {
		t.Parallel()

		query := testCollection().With(orders).With(orders)
		if len(query.with) != 2 {
			t.Errorf("with = %d relations, want 2", len(query.with))
		}
	})

	t.Run("rejects a nil relation", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().With(nil))
	})

	t.Run("rejects an incomplete declaration before any I/O", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().With(HasMany[User, order]{ForeignKey: "user_id"}))
	})
}
