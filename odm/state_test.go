package odm

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// tracked has an omitempty field (so a value can go away entirely) and a
// field kept out of the document (so it can never be a change).
type tracked struct {
	Model `bson:",inline"`

	Name     string   `bson:"name"`
	Nickname string   `bson:"nickname,omitempty"`
	Loaded   []string `bson:"-"`
}

func (tracked) CollectionName() string { return "tracked" }

func newTracked(t *testing.T, model *tracked) *tracked {
	t.Helper()

	model.ID = "id-1"
	if err := snapshot(model); err != nil {
		t.Fatalf("snapshot() error = %v", err)
	}
	return model
}

func changesOrFail(t *testing.T, model *tracked) (bson.M, []string) {
	t.Helper()

	changes, err := Changes(model)
	if err != nil {
		t.Fatalf("Changes() error = %v", err)
	}
	return changes.Set, changes.Unset
}

func TestIsPersisted(t *testing.T) {
	t.Parallel()

	t.Run("a model you built does not exist, whatever its ID", func(t *testing.T) {
		t.Parallel()

		model := tracked{Name: "Nana"}
		model.ID = "looks-real"
		if IsPersisted(&model) {
			t.Error("IsPersisted() = true for a model that was never read or written")
		}
	})

	t.Run("a snapshotted model exists", func(t *testing.T) {
		t.Parallel()

		if !IsPersisted(newTracked(t, &tracked{Name: "Nana"})) {
			t.Error("IsPersisted() = false after a snapshot")
		}
	})

	t.Run("a model with nowhere to keep state never exists", func(t *testing.T) {
		t.Parallel()

		if IsPersisted(&plainDoc{ID: "a"}) {
			t.Error("IsPersisted() = true for a model embedding neither base")
		}
	})
}

func TestChanges(t *testing.T) {
	t.Parallel()

	t.Run("a freshly read model is clean", func(t *testing.T) {
		t.Parallel()

		set, unset := changesOrFail(t, newTracked(t, &tracked{Name: "Nana"}))
		if len(set) != 0 || len(unset) != 0 {
			t.Errorf("Changes() = %v / %v, want none", set, unset)
		}
	})

	t.Run("reports a changed field", func(t *testing.T) {
		t.Parallel()

		model := newTracked(t, &tracked{Name: "Nana"})
		model.Name = "Nana Kwesi"

		set, unset := changesOrFail(t, model)
		if want := (bson.M{"name": "Nana Kwesi"}); !reflect.DeepEqual(set, want) {
			t.Errorf("set = %v, want %v", set, want)
		}
		if len(unset) != 0 {
			t.Errorf("unset = %v, want none", unset)
		}
	})

	t.Run("reports a field that appeared", func(t *testing.T) {
		t.Parallel()

		model := newTracked(t, &tracked{Name: "Nana"})
		model.Nickname = "NK"

		set, _ := changesOrFail(t, model)
		if want := (bson.M{"nickname": "NK"}); !reflect.DeepEqual(set, want) {
			t.Errorf("set = %v, want %v", set, want)
		}
	})

	t.Run("reports an omitempty field that went away as an unset", func(t *testing.T) {
		t.Parallel()

		model := newTracked(t, &tracked{Name: "Nana", Nickname: "NK"})
		model.Nickname = ""

		set, unset := changesOrFail(t, model)
		if len(set) != 0 {
			t.Errorf("set = %v, want none", set)
		}
		if want := []string{"nickname"}; !reflect.DeepEqual(unset, want) {
			t.Errorf("unset = %v, want %v", unset, want)
		}
	})

	t.Run("never reports _id", func(t *testing.T) {
		t.Parallel()

		model := newTracked(t, &tracked{Name: "Nana"})
		model.ID = "a-different-id"

		set, unset := changesOrFail(t, model)
		if _, ok := set["_id"]; ok {
			t.Errorf("set = %v, want _id left out — it identifies the document, it isn't part of it", set)
		}
		if len(unset) != 0 {
			t.Errorf("unset = %v, want none", unset)
		}
	})

	t.Run("ignores a field the document never carries", func(t *testing.T) {
		t.Parallel()

		model := newTracked(t, &tracked{Name: "Nana"})
		model.Loaded = []string{"a relation, say"}

		set, unset := changesOrFail(t, model)
		if len(set) != 0 || len(unset) != 0 {
			t.Errorf("Changes() = %v / %v, want a bson:- field to be no change at all", set, unset)
		}
	})

	t.Run("a model that does not exist yet reports nothing", func(t *testing.T) {
		t.Parallel()

		set, unset := changesOrFail(t, &tracked{Name: "Nana"})
		if set != nil || unset != nil {
			t.Errorf("Changes() = %v / %v, want none — everything about a new model is new", set, unset)
		}
	})

	t.Run("an untracked model reports nothing", func(t *testing.T) {
		t.Parallel()

		changes, err := Changes(&plainDoc{ID: "a", Name: "Nana"})
		if err != nil {
			t.Fatalf("Changes() error = %v", err)
		}
		if changes.Set != nil || changes.Unset != nil {
			t.Errorf("Changes() = %+v, want none", changes)
		}
	})
}

func TestIsDirty(t *testing.T) {
	t.Parallel()

	model := newTracked(t, &tracked{Name: "Nana", Nickname: "NK"})
	model.Name = "Nana Kwesi"

	assert := func(t *testing.T, want bool, fields ...string) {
		t.Helper()

		got, err := IsDirty(model, fields...)
		if err != nil {
			t.Fatalf("IsDirty() error = %v", err)
		}
		if got != want {
			t.Errorf("IsDirty(%v) = %t, want %t", fields, got, want)
		}
	}

	t.Run("without field names", func(t *testing.T) { assert(t, true) })
	t.Run("for the changed field", func(t *testing.T) { assert(t, true, "name") })
	t.Run("for an unchanged field", func(t *testing.T) { assert(t, false, "nickname") })
	t.Run("for any of several", func(t *testing.T) { assert(t, true, "nickname", "name") })

	t.Run("a clean model is not dirty", func(t *testing.T) {
		t.Parallel()

		clean := newTracked(t, &tracked{Name: "Nana"})
		got, err := IsDirty(clean)
		if err != nil {
			t.Fatalf("IsDirty() error = %v", err)
		}
		if got {
			t.Error("IsDirty() = true for an unchanged model")
		}
	})

	t.Run("a removed field counts", func(t *testing.T) {
		t.Parallel()

		removed := newTracked(t, &tracked{Name: "Nana", Nickname: "NK"})
		removed.Nickname = ""

		got, err := IsDirty(removed, "nickname")
		if err != nil {
			t.Fatalf("IsDirty() error = %v", err)
		}
		if !got {
			t.Error("IsDirty(\"nickname\") = false after the field was cleared")
		}
	})
}

func TestOriginal(t *testing.T) {
	t.Parallel()

	model := newTracked(t, &tracked{Name: "Nana"})
	model.Name = "Nana Kwesi"

	value, ok := Original(model, "name")
	if !ok {
		t.Fatal("Original() reported no value for a field the snapshot carries")
	}
	var was string
	if err := value.Unmarshal(&was); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if was != "Nana" {
		t.Errorf("Original() = %q, want the value before the change", was)
	}

	if _, ok := Original(model, "nope"); ok {
		t.Error("Original() reported a value for a field the snapshot has no record of")
	}

	if _, ok := Original(&tracked{Name: "new"}, "name"); ok {
		t.Error("Original() reported a value for a model that was never read")
	}
}

func TestDiffSkipsUnknownDocumentFields(t *testing.T) {
	t.Parallel()

	// The snapshot is of the model, not of the document it was decoded
	// from, so a field the collection carries and the struct does not can
	// never end up in an unset. This is what stops Save from deleting data
	// it cannot see.
	model := newTracked(t, &tracked{Name: "Nana"})
	model.Name = "Nana Kwesi"

	set, unset := changesOrFail(t, model)
	if len(unset) != 0 {
		t.Errorf("unset = %v, want none", unset)
	}
	if len(set) != 1 {
		t.Errorf("set = %v, want only the changed field", set)
	}
}
