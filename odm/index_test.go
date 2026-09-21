package odm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// indexArgs applies an Index's compiled options to a fresh IndexOptions, so
// a test asserts on the driver's own option struct rather than on this
// package's declaration.
func indexArgs(t *testing.T, index Index) (mongo.IndexModel, *options.IndexOptions) {
	t.Helper()

	model, err := index.model()
	if err != nil {
		t.Fatalf("model() error = %v", err)
	}

	args := &options.IndexOptions{}
	for _, setter := range model.Options.List() {
		if err := setter(args); err != nil {
			t.Fatalf("applying index option: %v", err)
		}
	}
	return model, args
}

func TestIndexModel(t *testing.T) {
	t.Parallel()

	t.Run("carries the keys through in order", func(t *testing.T) {
		t.Parallel()

		keys := bson.D{{Key: "business_id", Value: 1}, {Key: "created_at", Value: -1}}
		model, _ := indexArgs(t, Index{Keys: keys})
		if !reflect.DeepEqual(model.Keys, keys) {
			t.Errorf("Keys = %v, want %v", model.Keys, keys)
		}
	})

	t.Run("sets only the options that were asked for", func(t *testing.T) {
		t.Parallel()

		_, args := indexArgs(t, Index{Keys: bson.D{{Key: "email", Value: 1}}})
		switch {
		case args.Unique != nil:
			t.Errorf("Unique = %v on a bare index, want it unset", *args.Unique)
		case args.Sparse != nil:
			t.Errorf("Sparse = %v on a bare index, want it unset", *args.Sparse)
		case args.Name != nil:
			t.Errorf("Name = %q on a bare index, want MongoDB's generated one", *args.Name)
		case args.ExpireAfterSeconds != nil:
			t.Errorf("ExpireAfterSeconds = %d on a bare index, want it unset", *args.ExpireAfterSeconds)
		case args.PartialFilterExpression != nil:
			t.Errorf("PartialFilterExpression = %v on a bare index, want it unset", args.PartialFilterExpression)
		}
	})

	t.Run("maps each declared option", func(t *testing.T) {
		t.Parallel()

		partial := bson.M{deletedAtField: nil}
		_, args := indexArgs(t, Index{
			Keys:          bson.D{{Key: "email", Value: 1}},
			Name:          "email_unique",
			Unique:        true,
			Sparse:        true,
			ExpireAfter:   90 * time.Minute,
			PartialFilter: partial,
		})

		if args.Name == nil || *args.Name != "email_unique" {
			t.Errorf("Name = %v, want %q", args.Name, "email_unique")
		}
		if args.Unique == nil || !*args.Unique {
			t.Errorf("Unique = %v, want true", args.Unique)
		}
		if args.Sparse == nil || !*args.Sparse {
			t.Errorf("Sparse = %v, want true", args.Sparse)
		}
		// MongoDB's TTL resolution is whole seconds.
		if args.ExpireAfterSeconds == nil || *args.ExpireAfterSeconds != 5400 {
			t.Errorf("ExpireAfterSeconds = %v, want 5400", args.ExpireAfterSeconds)
		}
		if !reflect.DeepEqual(args.PartialFilterExpression, partial) {
			t.Errorf("PartialFilterExpression = %v, want %v", args.PartialFilterExpression, partial)
		}
	})

	t.Run("rejects an index with no keys", func(t *testing.T) {
		t.Parallel()

		if _, err := (Index{Unique: true}).model(); err == nil {
			t.Error("model() error = nil, want a complaint about the missing keys")
		}
	})

	t.Run("rejects a negative expiry", func(t *testing.T) {
		t.Parallel()

		index := Index{Keys: bson.D{{Key: "created_at", Value: 1}}, ExpireAfter: -time.Hour}
		if _, err := index.model(); err == nil {
			t.Error("model() error = nil, want a complaint about ExpireAfter")
		}
	})

	t.Run("names the offending index in a batch", func(t *testing.T) {
		t.Parallel()

		_, err := indexModels([]Index{
			{Keys: bson.D{{Key: "email", Value: 1}}},
			{Unique: true},
		})
		if err == nil || !strings.Contains(err.Error(), "index 1") {
			t.Errorf("indexModels() error = %v, want it to name index 1", err)
		}
	})
}

func TestSyncIndexesRequiresADeclaration(t *testing.T) {
	t.Parallel()

	// User declares none, so this fails before touching the server.
	err := (&Collection[User]{meta: metaFor[User]()}).SyncIndexes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "odm.Indexer") {
		t.Errorf("SyncIndexes() error = %v, want it to point at odm.Indexer", err)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	t.Run("passes nil through", func(t *testing.T) {
		t.Parallel()

		if got := classify(nil); got != nil {
			t.Errorf("classify(nil) = %v, want nil", got)
		}
	})

	t.Run("passes an unrelated error through untouched", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("connection reset")
		got := classify(sentinel)
		if !errors.Is(got, sentinel) || errors.Is(got, ErrDuplicateKey) {
			t.Errorf("classify() = %v, want the original error and not ErrDuplicateKey", got)
		}
	})

	t.Run("names a duplicate key and keeps the driver's own error", func(t *testing.T) {
		t.Parallel()

		driverErr := mongo.WriteException{
			WriteErrors: mongo.WriteErrors{{
				Code:    11000,
				Message: `E11000 duplicate key error collection: app.users index: email_1`,
			}},
		}

		got := classify(driverErr)
		if !errors.Is(got, ErrDuplicateKey) {
			t.Fatalf("classify() = %v, want ErrDuplicateKey", got)
		}

		var write mongo.WriteException
		if !errors.As(got, &write) {
			t.Fatal("classify() lost the driver's *mongo.WriteException")
		}
		if !strings.Contains(write.WriteErrors[0].Message, "email_1") {
			t.Errorf("driver message = %q, want the constraint name kept", write.WriteErrors[0].Message)
		}
	})

	t.Run("names the other duplicate-key codes too", func(t *testing.T) {
		t.Parallel()

		for _, code := range []int{11000, 11001, 12582} {
			err := mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: code}}}
			if !errors.Is(classify(err), ErrDuplicateKey) {
				t.Errorf("classify(code %d) did not report ErrDuplicateKey", code)
			}
		}
	})
}
