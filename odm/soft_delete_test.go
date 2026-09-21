package odm

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// softUser is the unit suite's soft-deleting model.
type softUser struct {
	Model       `bson:",inline"`
	SoftDeletes `bson:",inline"`

	Name string `bson:"name"`
}

func (softUser) CollectionName() string { return "soft_users" }

func testSoftCollection() *Collection[softUser] {
	return &Collection[softUser]{meta: metaFor[softUser](), now: func() time.Time { return testNow }}
}

func TestSoftDeleteScope(t *testing.T) {
	t.Parallel()

	notDeleted := bson.M{deletedAtField: nil}
	deleted := bson.M{deletedAtField: bson.M{"$ne": nil}}

	t.Run("hides soft-deleted documents by default", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Where("name", "Nana").filter()
		want := any(bson.M{"$and": bson.A{bson.M{"name": "Nana"}, notDeleted}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("scopes an otherwise empty query", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Query().filter()
		if !reflect.DeepEqual(got, any(notDeleted)) {
			t.Errorf("filter() = %v, want %v", got, notDeleted)
		}
	})

	t.Run("WithTrashed drops the scope entirely", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Where("name", "Nana").WithTrashed().filter()
		want := any(bson.M{"name": "Nana"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("OnlyTrashed inverts it", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Where("name", "Nana").OnlyTrashed().filter()
		want := any(bson.M{"$and": bson.A{bson.M{"name": "Nana"}, deleted}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("overrides the default whatever order the chain is written in", func(t *testing.T) {
		t.Parallel()

		before := testSoftCollection().WithTrashed().Where("name", "Nana").filter()
		after := testSoftCollection().Where("name", "Nana").WithTrashed().filter()
		if !reflect.DeepEqual(before, after) {
			t.Errorf("WithTrashed first = %v, last = %v, want the same filter", before, after)
		}
	})

	t.Run("lands exactly once however many conditions there are", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Where("a", 1).Where("b", 2).filter()
		want := any(bson.M{"$and": bson.A{bson.M{"a": 1}, bson.M{"b": 2}, notDeleted}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("leaves a model without soft deletes unscoped", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("name", "Nana").filter()
		want := any(bson.M{"name": "Nana"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("branches keep independent trashed modes", func(t *testing.T) {
		t.Parallel()

		base := testSoftCollection().Where("name", "Nana")
		all := base.WithTrashed()

		if base.trashed != trashedExcluded {
			t.Errorf("base trashed = %v, want the default", base.trashed)
		}
		if all.trashed != trashedIncluded {
			t.Errorf("branch trashed = %v, want included", all.trashed)
		}
	})
}

func TestSoftDeleteRequiresTheCapability(t *testing.T) {
	t.Parallel()

	// Every soft-delete-only call on a model that doesn't embed
	// odm.SoftDeletes has to fail rather than filter or stamp a field the
	// model has nowhere to keep.
	assertInvalidQuery(t, testCollection().WithTrashed())
	assertInvalidQuery(t, testCollection().OnlyTrashed())

	ctx := t.Context()
	if _, err := testCollection().Query().Restore(ctx); err == nil {
		t.Error("Restore() error = nil, want ErrInvalidQuery")
	}
	if _, err := testCollection().Query().ForceDelete(ctx); err == nil {
		t.Error("ForceDelete() error = nil, want ErrInvalidQuery")
	}
}

func TestForceDeleteMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode trashedMode
		want trashedMode
	}{
		{"the default scope is ignored, so trashed documents are purged too", trashedExcluded, trashedIncluded},
		{"an explicit WithTrashed is already what it wants", trashedIncluded, trashedIncluded},
		{"OnlyTrashed is honoured, purging just those", trashedOnly, trashedOnly},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := forceDeleteMode(test.mode); got != test.want {
				t.Errorf("forceDeleteMode(%v) = %v, want %v", test.mode, got, test.want)
			}
		})
	}
}

func TestRestoreCompilation(t *testing.T) {
	t.Parallel()

	t.Run("targets only soft-deleted documents, whatever the query asked for", func(t *testing.T) {
		t.Parallel()

		deleted := bson.M{deletedAtField: bson.M{"$ne": nil}}
		for _, query := range []*Query[softUser]{
			testSoftCollection().Where("name", "Nana"),
			testSoftCollection().Where("name", "Nana").WithTrashed(),
			testSoftCollection().Where("name", "Nana").OnlyTrashed(),
		} {
			got := query.compileFilter(trashedOnly)
			want := any(bson.M{"$and": bson.A{bson.M{"name": "Nana"}, deleted}})
			if !reflect.DeepEqual(got, want) {
				t.Errorf("compileFilter() = %v, want %v", got, want)
			}
		}
	})

	t.Run("clears the stamp and refreshes updated_at", func(t *testing.T) {
		t.Parallel()

		got := compileUpdate(testSoftCollection().Query().restoreOps())
		want := bson.D{
			{Key: "$unset", Value: bson.D{{Key: deletedAtField, Value: ""}}},
			{Key: "$set", Value: bson.D{{Key: updatedAtField, Value: testNow}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("WithoutTimestamps clears the stamp alone", func(t *testing.T) {
		t.Parallel()

		got := compileUpdate(testSoftCollection().Query().WithoutTimestamps().restoreOps())
		want := bson.D{{Key: "$unset", Value: bson.D{{Key: deletedAtField, Value: ""}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})
}

func TestSoftDeleteUpdate(t *testing.T) {
	t.Parallel()

	t.Run("stamps deleted_at and updated_at from one instant", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Query().softDeleteUpdate()
		want := bson.D{{Key: "$set", Value: bson.D{
			{Key: deletedAtField, Value: testNow},
			{Key: updatedAtField, Value: testNow},
		}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("WithoutTimestamps stamps deleted_at alone", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Query().WithoutTimestamps().softDeleteUpdate()
		want := bson.D{{Key: "$set", Value: bson.D{{Key: deletedAtField, Value: testNow}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})
}
