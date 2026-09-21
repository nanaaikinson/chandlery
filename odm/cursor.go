package odm

import (
	"encoding/base64"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// cursorVersion is stamped into every cursor, so a cursor minted by an older
// build is rejected outright rather than decoded into a shape this one would
// misread.
const cursorVersion = 1

// cursorPayload is what a cursor string carries: the sort it was produced
// under, and the sort-key values of the last document on the page. Keeping
// the sort alongside the keys is what lets a cursor be checked against the
// query it is handed to — a cursor from a created_at DESC page would
// silently produce nonsense against an ASC one.
//
// Marshalled as BSON rather than JSON so a key keeps its exact type: an
// ObjectID stays an ObjectID and a date stays a date, which the keyset
// comparison depends on.
type cursorPayload struct {
	Version int    `bson:"v"`
	Sorts   bson.D `bson:"s"`
	Keys    bson.D `bson:"k"`
}

// encodeCursor renders a payload as one opaque, URL-safe string. Callers get
// something to hand back, not something to take apart — the encoding is this
// package's business and may change.
func encodeCursor(sorts, keys bson.D) (string, error) {
	raw, err := bson.Marshal(cursorPayload{Version: cursorVersion, Sorts: sorts, Keys: keys})
	if err != nil {
		return "", fmt.Errorf("odm: encoding cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor parses a cursor and checks it was produced under sorts. Every
// failure is ErrInvalidCursor: a caller handling a stale or hand-edited
// cursor from a URL wants one branch, not five.
func decodeCursor(cursor string, sorts bson.D) (bson.D, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base64: %w", ErrInvalidCursor, err)
	}

	var payload cursorPayload
	if err := bson.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: not a valid cursor document: %w", ErrInvalidCursor, err)
	}
	if payload.Version != cursorVersion {
		return nil, fmt.Errorf("%w: version %d, want %d", ErrInvalidCursor, payload.Version, cursorVersion)
	}
	if len(payload.Keys) != len(payload.Sorts) {
		return nil, fmt.Errorf("%w: %d sort key(s) for %d sort field(s)", ErrInvalidCursor, len(payload.Keys), len(payload.Sorts))
	}
	if err := sameSort(payload.Sorts, sorts); err != nil {
		return nil, err
	}
	return payload.Keys, nil
}

// sameSort reports whether a cursor's sort matches the query's, field for
// field and direction for direction.
func sameSort(cursorSorts, sorts bson.D) error {
	if len(cursorSorts) != len(sorts) {
		return fmt.Errorf("%w: cursor sorts on %d field(s), the query on %d", ErrInvalidCursor, len(cursorSorts), len(sorts))
	}
	for i := range sorts {
		if cursorSorts[i].Key != sorts[i].Key {
			return fmt.Errorf("%w: cursor sorts on %q where the query sorts on %q", ErrInvalidCursor, cursorSorts[i].Key, sorts[i].Key)
		}
		if !sameDirection(cursorSorts[i].Value, sorts[i].Value) {
			return fmt.Errorf("%w: cursor sorts %q the other way round", ErrInvalidCursor, sorts[i].Key)
		}
	}
	return nil
}

// sameDirection compares two sort directions. A direction survives a BSON
// round trip as an int32 while the query holds an int, so both are widened
// before comparing.
func sameDirection(a, b any) bool {
	return sortDirection(a) == sortDirection(b)
}

func sortDirection(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	default:
		return 0
	}
}

// cursorKeys reads the sort-key values out of a document as it came off the
// wire, rather than off the decoded model: the sort field may not be a field
// of T at all, and reading the raw bytes keeps the value's exact BSON type
// for the next page's comparison.
func cursorKeys(raw bson.Raw, sorts bson.D) (bson.D, error) {
	keys := make(bson.D, len(sorts))
	for i, sort := range sorts {
		// Dotted paths address embedded documents, the same as in a filter.
		value, err := raw.LookupErr(strings.Split(sort.Key, ".")...)
		if err != nil {
			return nil, fmt.Errorf("%w: CursorPaginate: sort field %q is missing from the returned document — a projection must keep every field the sort uses", ErrInvalidQuery, sort.Key)
		}

		var decoded any
		if err := value.Unmarshal(&decoded); err != nil {
			return nil, fmt.Errorf("odm: CursorPaginate: reading sort field %q: %w", sort.Key, err)
		}
		keys[i] = bson.E{Key: sort.Key, Value: decoded}
	}
	return keys, nil
}

// paginationSorts makes a sort stable by appending _id when nothing unique
// is already in it. Without a unique tie-breaker, documents sharing a sort
// value have no defined order between them, and a keyset cursor over them
// would skip or repeat rows as pages are fetched.
//
// An unsorted query paginates by _id alone, ascending.
func paginationSorts(sorts bson.D) bson.D {
	for _, sort := range sorts {
		if sort.Key == "_id" {
			return sorts
		}
	}

	// Match the last sort's direction, so _id breaks ties the same way the
	// rest of the ordering runs.
	direction := int(Asc)
	if len(sorts) > 0 {
		direction = int(sortDirection(sorts[len(sorts)-1].Value))
	}
	return cloneAppend(sorts, bson.E{Key: "_id", Value: direction})
}

// keysetFilter builds the "everything after this document" condition for a
// sort of any width:
//
//	f1 > v1
//	OR (f1 == v1 AND f2 > v2)
//	OR (f1 == v1 AND f2 == v2 AND _id > vid)
//
// with > flipped to < on a descending field. The equality prefixes are what
// make it exact where sort values repeat — the reason a plain "f1 > v1"
// cursor silently drops rows.
func keysetFilter(sorts, keys bson.D) bson.M {
	branches := make(bson.A, 0, len(sorts))

	for i, sort := range sorts {
		conditions := make([]any, 0, i+1)
		for j := range i {
			conditions = append(conditions, bson.M{sorts[j].Key: keys[j].Value})
		}

		operator := "$gt"
		if sortDirection(sort.Value) < 0 {
			operator = "$lt"
		}
		conditions = append(conditions, bson.M{sort.Key: bson.M{operator: keys[i].Value}})

		branches = append(branches, buildFilter(conditions))
	}

	return bson.M{"$or": branches}
}
