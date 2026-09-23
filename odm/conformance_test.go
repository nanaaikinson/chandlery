package odm

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestModelID(t *testing.T) {
	t.Parallel()

	t.Run("reads the _id a model carries", func(t *testing.T) {
		t.Parallel()

		model := &tracked{Name: "Nana"}
		model.ID = bson.NewObjectID()

		id, ok := modelID(model)
		if !ok {
			t.Fatal("modelID() found no _id on a model that has one")
		}
		if id != model.ID {
			t.Errorf("modelID() = %v, want %v", id, model.ID)
		}
	})

	t.Run("reports none when the model has no _id field", func(t *testing.T) {
		t.Parallel()

		type idless struct {
			Name string `bson:"name"`
		}
		if _, ok := modelID(&idless{Name: "Nana"}); ok {
			t.Error("modelID() found an _id on a model that has no such field")
		}
	})
}

// TestNonInlineEmbeddingLosesFields pins down the mistake TestConformance's
// round-trip check exists to catch. Embedding odm.Model without
// `bson:",inline"` nests its fields under a "Model" key instead of putting
// them at the top level, so the document has no _id, no created_at and no
// updated_at where anything looks for them — and every query still compiles,
// which is what makes it worth a test.
func TestNonInlineEmbeddingLosesFields(t *testing.T) {
	t.Parallel()

	type broken struct {
		Model // the missing tag

		Name string `bson:"name"`
	}

	marshalled, err := bson.Marshal(&broken{Name: "Nana"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	raw := bson.Raw(marshalled)

	if _, err := raw.LookupErr("_id"); err == nil {
		t.Fatal("a non-inline embedded Model put _id at the top level after all — this test is no longer describing anything")
	}

	// It is there, just in the wrong place — nested under the embedded
	// field's own name, lowercased by the driver's default naming — which
	// is why nothing errors and the mistake survives to production.
	if _, err := raw.LookupErr("model", "_id"); err != nil {
		t.Errorf("_id is neither at the top level nor nested under \"model\": %v", err)
	}

	// And the correctly tagged version does put it where it belongs, which
	// is what the conformance suite compares against.
	type correct struct {
		Model `bson:",inline"`

		Name string `bson:"name"`
	}
	marshalled, err = bson.Marshal(&correct{Name: "Nana"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := bson.Raw(marshalled).LookupErr("_id"); err != nil {
		t.Errorf("an inline embedded Model still hid _id: %v", err)
	}
}
