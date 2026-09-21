package odm

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestPaginationSorts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sorts bson.D
		want  bson.D
	}{
		{
			name:  "appends _id to break ties on a non-unique field",
			sorts: bson.D{{Key: "created_at", Value: -1}},
			want:  bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}},
		},
		{
			name:  "matches the last sort's direction",
			sorts: bson.D{{Key: "created_at", Value: -1}, {Key: "name", Value: 1}},
			want:  bson.D{{Key: "created_at", Value: -1}, {Key: "name", Value: 1}, {Key: "_id", Value: 1}},
		},
		{
			name:  "leaves a sort that already ends in _id alone",
			sorts: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}},
			want:  bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}},
		},
		{
			name:  "leaves _id alone wherever it appears",
			sorts: bson.D{{Key: "_id", Value: 1}, {Key: "name", Value: -1}},
			want:  bson.D{{Key: "_id", Value: 1}, {Key: "name", Value: -1}},
		},
		{
			name:  "paginates an unsorted query by _id ascending",
			sorts: nil,
			want:  bson.D{{Key: "_id", Value: 1}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := paginationSorts(test.sorts); !reflect.DeepEqual(got, test.want) {
				t.Errorf("paginationSorts(%v) = %v, want %v", test.sorts, got, test.want)
			}
		})
	}
}

func TestKeysetFilter(t *testing.T) {
	t.Parallel()

	t.Run("ascending seeks forward", func(t *testing.T) {
		t.Parallel()

		got := keysetFilter(
			bson.D{{Key: "_id", Value: 1}},
			bson.D{{Key: "_id", Value: "abc"}},
		)
		want := bson.M{"$or": bson.A{bson.M{"_id": bson.M{"$gt": "abc"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keysetFilter() = %v, want %v", got, want)
		}
	})

	t.Run("descending seeks backward", func(t *testing.T) {
		t.Parallel()

		got := keysetFilter(
			bson.D{{Key: "_id", Value: -1}},
			bson.D{{Key: "_id", Value: "abc"}},
		)
		want := bson.M{"$or": bson.A{bson.M{"_id": bson.M{"$lt": "abc"}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keysetFilter() = %v, want %v", got, want)
		}
	})

	t.Run("equality prefixes make repeated sort values exact", func(t *testing.T) {
		t.Parallel()

		got := keysetFilter(
			bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}},
			bson.D{{Key: "created_at", Value: "t"}, {Key: "_id", Value: "abc"}},
		)
		want := bson.M{"$or": bson.A{
			bson.M{"created_at": bson.M{"$lt": "t"}},
			bson.M{"$and": bson.A{
				bson.M{"created_at": "t"},
				bson.M{"_id": bson.M{"$lt": "abc"}},
			}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keysetFilter() = %v, want %v", got, want)
		}
	})

	t.Run("mixed directions each keep their own comparison", func(t *testing.T) {
		t.Parallel()

		got := keysetFilter(
			bson.D{{Key: "score", Value: -1}, {Key: "_id", Value: 1}},
			bson.D{{Key: "score", Value: 10}, {Key: "_id", Value: "abc"}},
		)
		want := bson.M{"$or": bson.A{
			bson.M{"score": bson.M{"$lt": 10}},
			bson.M{"$and": bson.A{
				bson.M{"score": 10},
				bson.M{"_id": bson.M{"$gt": "abc"}},
			}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keysetFilter() = %v, want %v", got, want)
		}
	})
}

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	sorts := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}

	t.Run("decodes back to the keys it encoded", func(t *testing.T) {
		t.Parallel()

		keys := bson.D{{Key: "created_at", Value: "2026-01-01"}, {Key: "_id", Value: "abc"}}
		cursor, err := encodeCursor(sorts, keys)
		if err != nil {
			t.Fatalf("encodeCursor() error = %v", err)
		}

		got, err := decodeCursor(cursor, sorts)
		if err != nil {
			t.Fatalf("decodeCursor() error = %v", err)
		}
		if !reflect.DeepEqual(got, keys) {
			t.Errorf("decodeCursor() = %v, want %v", got, keys)
		}
	})

	t.Run("is URL-safe and unpadded", func(t *testing.T) {
		t.Parallel()

		cursor, err := encodeCursor(sorts, bson.D{
			{Key: "created_at", Value: "2026-01-01"},
			{Key: "_id", Value: "abc"},
		})
		if err != nil {
			t.Fatalf("encodeCursor() error = %v", err)
		}
		if _, err := base64.RawURLEncoding.DecodeString(cursor); err != nil {
			t.Errorf("cursor %q is not raw-url base64: %v", cursor, err)
		}
	})

	t.Run("keeps a key's BSON type", func(t *testing.T) {
		t.Parallel()

		id := bson.NewObjectID()
		typed := bson.D{{Key: "_id", Value: id}}
		idSort := bson.D{{Key: "_id", Value: -1}}

		cursor, err := encodeCursor(idSort, typed)
		if err != nil {
			t.Fatalf("encodeCursor() error = %v", err)
		}

		got, err := decodeCursor(cursor, idSort)
		if err != nil {
			t.Fatalf("decodeCursor() error = %v", err)
		}
		if got[0].Value != id {
			t.Errorf("decoded _id = %v (%T), want %v (%T)", got[0].Value, got[0].Value, id, id)
		}
	})
}

func TestInvalidCursor(t *testing.T) {
	t.Parallel()

	sorts := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	valid, err := encodeCursor(sorts, bson.D{
		{Key: "created_at", Value: "t"},
		{Key: "_id", Value: "abc"},
	})
	if err != nil {
		t.Fatalf("encodeCursor() error = %v", err)
	}

	t.Run("rejects text that isn't base64", func(t *testing.T) {
		t.Parallel()

		if _, err := decodeCursor("not a cursor!!", sorts); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("rejects base64 that isn't a cursor document", func(t *testing.T) {
		t.Parallel()

		junk := base64.RawURLEncoding.EncodeToString([]byte("definitely not bson"))
		if _, err := decodeCursor(junk, sorts); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("rejects a cursor from another version", func(t *testing.T) {
		t.Parallel()

		raw, err := bson.Marshal(cursorPayload{Version: cursorVersion + 1, Sorts: sorts, Keys: sorts})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		stale := base64.RawURLEncoding.EncodeToString(raw)
		if _, err := decodeCursor(stale, sorts); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("rejects a cursor minted under a different field", func(t *testing.T) {
		t.Parallel()

		other := bson.D{{Key: "name", Value: -1}, {Key: "_id", Value: -1}}
		if _, err := decodeCursor(valid, other); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("rejects a cursor minted under the opposite direction", func(t *testing.T) {
		t.Parallel()

		flipped := bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}
		if _, err := decodeCursor(valid, flipped); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("rejects a cursor with a different number of keys", func(t *testing.T) {
		t.Parallel()

		narrower := bson.D{{Key: "created_at", Value: -1}}
		if _, err := decodeCursor(valid, narrower); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor() error = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("surfaces through CursorPaginate", func(t *testing.T) {
		t.Parallel()

		_, err := testCollection().
			OrderBy("created_at", Desc).
			CursorPaginate(context.Background(), CursorPagination{Limit: 10, Cursor: "bogus!!"})
		if !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("CursorPaginate() error = %v, want ErrInvalidCursor", err)
		}
	})
}

func TestCursorPaginateValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	for _, limit := range []int64{0, -1} {
		_, err := testCollection().CursorPaginate(ctx, CursorPagination{Limit: limit})
		if !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("CursorPaginate(Limit: %d) error = %v, want ErrInvalidQuery", limit, err)
		}
	}

	if _, err := testCollection().Limit(-1).CursorPaginate(ctx, CursorPagination{Limit: 10}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("CursorPaginate() error = %v, want the query's own recorded error", err)
	}
}

func TestSeekQueryImmutability(t *testing.T) {
	t.Parallel()

	sorts := bson.D{{Key: "_id", Value: 1}}
	cursor, err := encodeCursor(sorts, bson.D{{Key: "_id", Value: "abc"}})
	if err != nil {
		t.Fatalf("encodeCursor() error = %v", err)
	}

	base := testCollection().Where("active", true)

	first, err := base.seekQuery(sorts, "")
	if err != nil {
		t.Fatalf("seekQuery() error = %v", err)
	}
	if len(first.filters) != 1 {
		t.Errorf("first page has %d conditions, want the query's own 1", len(first.filters))
	}

	next, err := base.seekQuery(sorts, cursor)
	if err != nil {
		t.Fatalf("seekQuery() error = %v", err)
	}
	if len(next.filters) != 2 {
		t.Errorf("seeking page has %d conditions, want 2", len(next.filters))
	}
	if len(base.filters) != 1 {
		t.Errorf("base query grew to %d conditions, want it untouched at 1", len(base.filters))
	}
}

func TestScopedPipeline(t *testing.T) {
	t.Parallel()

	stage := bson.D{{Key: "$group", Value: bson.M{"_id": "$country"}}}

	t.Run("prepends the query's filter as a $match", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Where("status", "active").scopedPipeline(mongo.Pipeline{stage})
		want := mongo.Pipeline{
			bson.D{{Key: "$match", Value: bson.M{"status": "active"}}},
			stage,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scopedPipeline() = %v, want %v", got, want)
		}
	})

	t.Run("adds no stage when there is nothing to match on", func(t *testing.T) {
		t.Parallel()

		got := testCollection().Query().scopedPipeline(mongo.Pipeline{stage})
		want := mongo.Pipeline{stage}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scopedPipeline() = %v, want %v", got, want)
		}
	})

	t.Run("scopes a soft-delete model even unfiltered", func(t *testing.T) {
		t.Parallel()

		got := testSoftCollection().Query().scopedPipeline(mongo.Pipeline{stage})
		want := mongo.Pipeline{
			bson.D{{Key: "$match", Value: bson.M{deletedAtField: nil}}},
			stage,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scopedPipeline() = %v, want %v", got, want)
		}
	})

	t.Run("leaves the caller's pipeline alone", func(t *testing.T) {
		t.Parallel()

		pipeline := mongo.Pipeline{stage}
		testCollection().Where("status", "active").scopedPipeline(pipeline)

		if len(pipeline) != 1 || !reflect.DeepEqual(pipeline[0], stage) {
			t.Errorf("caller's pipeline = %v, want it untouched", pipeline)
		}
	})
}
