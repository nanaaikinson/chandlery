package odm

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// plainDoc embeds neither Model nor IdentityModel, so it has no timestamps
// for an update to refresh.
type plainDoc struct {
	ID   string `bson:"_id"`
	Name string `bson:"name"`
}

func (plainDoc) CollectionName() string { return "plain_docs" }

func testPlainCollection() *Collection[plainDoc] {
	return &Collection[plainDoc]{meta: metaFor[plainDoc](), now: func() time.Time { return testNow }}
}

func TestModelMetadata(t *testing.T) {
	t.Parallel()

	t.Run("odm.Model carries timestamps and no soft deletes", func(t *testing.T) {
		t.Parallel()

		meta := metaFor[User]()
		if !meta.timestamps || meta.softDeletes {
			t.Errorf("metaFor[User]() = %+v, want timestamps and no soft deletes", meta)
		}
	})

	t.Run("odm.IdentityModel carries neither", func(t *testing.T) {
		t.Parallel()

		type session struct {
			IdentityModel `bson:",inline"`

			Token string `bson:"token"`
		}

		meta := metaFor[session]()
		if meta.timestamps || meta.softDeletes {
			t.Errorf("metaFor[session]() = %+v, want neither", meta)
		}
	})

	t.Run("a plain struct carries neither", func(t *testing.T) {
		t.Parallel()

		meta := metaFor[plainDoc]()
		if meta.timestamps || meta.softDeletes {
			t.Errorf("metaFor[plainDoc]() = %+v, want neither", meta)
		}
	})

	t.Run("odm.SoftDeletes is detected", func(t *testing.T) {
		t.Parallel()

		meta := metaFor[softUser]()
		if !meta.timestamps || !meta.softDeletes {
			t.Errorf("metaFor[softUser]() = %+v, want both", meta)
		}
	})
}

func TestIdentityModelAssignsULID(t *testing.T) {
	t.Parallel()

	t.Run("assigns an id and leaves timestamps alone", func(t *testing.T) {
		t.Parallel()

		var model IdentityModel
		model.prepareForInsert(testNow)
		if model.ID == "" {
			t.Error("prepareForInsert() left ID empty, want a ULID")
		}
	})

	t.Run("keeps an id the caller already set", func(t *testing.T) {
		t.Parallel()

		model := IdentityModel{ID: "chosen"}
		model.prepareForInsert(testNow)
		if model.ID != "chosen" {
			t.Errorf("prepareForInsert() overwrote ID = %q, want %q", model.ID, "chosen")
		}
	})

	t.Run("Model stamps both timestamps and still assigns an id", func(t *testing.T) {
		t.Parallel()

		var model Model
		model.prepareForInsert(testNow)
		if model.ID == "" {
			t.Error("prepareForInsert() left ID empty, want a ULID")
		}
		if !model.CreatedAt.Equal(testNow) || !model.UpdatedAt.Equal(testNow) {
			t.Errorf("prepareForInsert() left %v/%v, want both %v", model.CreatedAt, model.UpdatedAt, testNow)
		}
	})

	t.Run("Model keeps a caller-supplied CreatedAt", func(t *testing.T) {
		t.Parallel()

		backfilled := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		model := Model{CreatedAt: backfilled}
		model.prepareForInsert(testNow)
		if !model.CreatedAt.Equal(backfilled) {
			t.Errorf("prepareForInsert() overwrote CreatedAt = %v, want %v", model.CreatedAt, backfilled)
		}
		if !model.UpdatedAt.Equal(testNow) {
			t.Errorf("prepareForInsert() left UpdatedAt = %v, want %v", model.UpdatedAt, testNow)
		}
	})
}

func TestUpdateTimestamps(t *testing.T) {
	t.Parallel()

	staged := func(t *testing.T, query *Query[User]) bson.D {
		t.Helper()

		update, err := query.stagedUpdate("Update")
		if err != nil {
			t.Fatalf("stagedUpdate() error = %v", err)
		}
		return update
	}

	t.Run("refreshes updated_at from the injected clock", func(t *testing.T) {
		t.Parallel()

		got := staged(t, testCollection().Query().Set("name", "Nana"))
		want := bson.D{{Key: "$set", Value: bson.D{
			{Key: "name", Value: "Nana"},
			{Key: updatedAtField, Value: testNow},
		}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("groups the stamp with an existing $set rather than conflicting", func(t *testing.T) {
		t.Parallel()

		got := staged(t, testCollection().Query().Inc("login_count", 1))
		want := bson.D{
			{Key: "$inc", Value: bson.D{{Key: "login_count", Value: 1}}},
			{Key: "$set", Value: bson.D{{Key: updatedAtField, Value: testNow}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("WithoutTimestamps opts out", func(t *testing.T) {
		t.Parallel()

		got := staged(t, testCollection().Query().Set("name", "Nana").WithoutTimestamps())
		want := bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: "Nana"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("defers to an explicit updated_at", func(t *testing.T) {
		t.Parallel()

		chosen := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		got := staged(t, testCollection().Query().Set(updatedAtField, chosen))
		want := bson.D{{Key: "$set", Value: bson.D{{Key: updatedAtField, Value: chosen}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update = %v, want %v", got, want)
		}
	})

	t.Run("leaves a model without timestamps alone", func(t *testing.T) {
		t.Parallel()

		query := testPlainCollection().Query().Set("name", "Nana")
		update, err := query.stagedUpdate("Update")
		if err != nil {
			t.Fatalf("stagedUpdate() error = %v", err)
		}
		want := bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: "Nana"}}}}
		if !reflect.DeepEqual(update, want) {
			t.Errorf("update = %v, want %v", update, want)
		}
	})

	t.Run("WithoutTimestamps leaves the query it branched from alone", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Query().Set("name", "Nana")
		bare := base.WithoutTimestamps()

		if len(staged(t, base)[0].Value.(bson.D)) != 2 {
			t.Error("base lost its automatic updated_at to a branch")
		}
		if len(staged(t, bare)[0].Value.(bson.D)) != 1 {
			t.Error("branch kept an updated_at it opted out of")
		}
	})
}

func TestScope(t *testing.T) {
	t.Parallel()

	active := func(q *Query[User]) *Query[User] { return q.Where("is_active", true) }
	verified := func(q *Query[User]) *Query[User] { return q.Where("verified", true) }

	t.Run("applies a scope", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Scope(active).filter()
		want := any(bson.M{"is_active": true})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("composes several in one call, left to right", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Scope(active, verified).filter()
		want := any(bson.M{"$and": bson.A{
			bson.M{"is_active": true},
			bson.M{"verified": true},
		}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("composes across chained calls and plain conditions", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Scope(active).Where("country", "GH").Scope(verified).filter()
		want := any(bson.M{"$and": bson.A{
			bson.M{"is_active": true},
			bson.M{"country": "GH"},
			bson.M{"verified": true},
		}})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter() = %v, want %v", got, want)
		}
	})

	t.Run("leaves the query it branched from alone", func(t *testing.T) {
		t.Parallel()

		base := testCollection().Where("country", "GH")
		scoped := base.Scope(active)

		if got, want := base.filter(), any(bson.M{"country": "GH"}); !reflect.DeepEqual(got, want) {
			t.Errorf("base filter() = %v, want %v", got, want)
		}
		if len(scoped.filters) != 2 {
			t.Errorf("scoped query has %d conditions, want 2", len(scoped.filters))
		}
	})

	t.Run("rejects a nil scope", func(t *testing.T) {
		t.Parallel()

		assertInvalidQuery(t, testCollection().Scope(nil))
		assertInvalidQuery(t, testCollection().Scope(active, nil))
	})

	t.Run("an empty call is a no-op", func(t *testing.T) {
		t.Parallel()

		query := testCollection().Scope()
		if query.err != nil {
			t.Errorf("err = %v, want nil", query.err)
		}
		if len(query.filters) != 0 {
			t.Errorf("filters = %v, want none", query.filters)
		}
	})
}

// hookedDoc records the order its hooks ran in, and can be told to fail
// either of them.
type hookedDoc struct {
	Model `bson:",inline"`

	Name string `bson:"name"`

	calls       []string `bson:"-"`
	idAtBefore  string   `bson:"-"`
	beforeError error    `bson:"-"`
	afterError  error    `bson:"-"`
}

func (hookedDoc) CollectionName() string { return "hooked_docs" }

func (h *hookedDoc) BeforeCreate(_ context.Context) error {
	h.calls = append(h.calls, "before")
	h.idAtBefore = h.ID
	return h.beforeError
}

func (h *hookedDoc) AfterCreate(_ context.Context) error {
	h.calls = append(h.calls, "after")
	return h.afterError
}

func TestCreateHookErrors(t *testing.T) {
	t.Parallel()

	// A failing BeforeCreate aborts before any I/O, so this needs no server
	// — which is also the guarantee being tested.
	t.Run("a BeforeCreate failure stops the insert", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("invalid")
		collection := &Collection[hookedDoc]{meta: metaFor[hookedDoc](), now: func() time.Time { return testNow }}
		model := hookedDoc{beforeError: sentinel}

		err := collection.Create(context.Background(), &model)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Create() error = %v, want it to wrap the hook's own error", err)
		}
		if reflect.DeepEqual(model.calls, []string{"before", "after"}) {
			t.Error("AfterCreate ran after BeforeCreate failed")
		}
		if model.ID != "" {
			t.Errorf("Create() assigned ID = %q despite the hook failing", model.ID)
		}
	})

	t.Run("a nil model is rejected before any hook", func(t *testing.T) {
		t.Parallel()

		collection := &Collection[hookedDoc]{meta: metaFor[hookedDoc]()}
		if err := collection.Create(context.Background(), nil); !errors.Is(err, ErrNilModel) {
			t.Errorf("Create() error = %v, want ErrNilModel", err)
		}
	})
}
