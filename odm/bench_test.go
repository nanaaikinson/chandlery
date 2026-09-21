package odm

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// These benchmarks cover the compilation paths every query runs through, so
// a regression in them shows up as a number rather than as a slow
// application. Nothing here touches MongoDB: the point is what this package
// costs on top of the driver, not what the driver costs.

func BenchmarkQueryBuild(b *testing.B) {
	collection := testCollection()

	b.ReportAllocs()
	for b.Loop() {
		_ = collection.
			Where("business_id", "b1").
			Where("age", ">=", 18).
			WhereIn("status", []string{"active", "pending"}).
			OrderBy("created_at", Desc).
			Limit(20)
	}
}

func BenchmarkQueryBranch(b *testing.B) {
	// Copy-on-write's whole cost is here: every branch off a shared base
	// copies the slice it extends.
	base := testCollection().Where("business_id", "b1").Where("active", true)

	b.ReportAllocs()
	for b.Loop() {
		_ = base.Where("role", "admin")
	}
}

func BenchmarkFilterCompile(b *testing.B) {
	query := testCollection().
		Where("business_id", "b1").
		Where("age", ">=", 18).
		WhereIn("status", []string{"active", "pending"})

	b.ReportAllocs()
	for b.Loop() {
		_ = query.filter()
	}
}

func BenchmarkFilterCompileSoftDeleted(b *testing.B) {
	query := testSoftCollection().Where("name", "Nana")

	b.ReportAllocs()
	for b.Loop() {
		_ = query.filter()
	}
}

func BenchmarkUpdateCompile(b *testing.B) {
	query := testCollection().Query().
		Set("name", "Nana").
		Set("email", "nana@example.com").
		Inc("login_count", 1).
		Unset("nickname")

	b.ReportAllocs()
	for b.Loop() {
		_ = compileUpdate(query.updates)
	}
}

func BenchmarkMetadataLookup(b *testing.B) {
	// Cached per type, so this is the cost every Use and every soft-delete
	// or timestamp decision pays.
	b.ReportAllocs()
	for b.Loop() {
		_ = metaFor[User]()
	}
}

func BenchmarkCursorEncode(b *testing.B) {
	sorts := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	keys := bson.D{{Key: "created_at", Value: "2026-01-01"}, {Key: "_id", Value: "01H0"}}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodeCursor(sorts, keys); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCursorDecode(b *testing.B) {
	sorts := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	cursor, err := encodeCursor(sorts, bson.D{
		{Key: "created_at", Value: "2026-01-01"},
		{Key: "_id", Value: "01H0"},
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := decodeCursor(cursor, sorts); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKeysetFilter(b *testing.B) {
	sorts := bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}
	keys := bson.D{{Key: "created_at", Value: "2026-01-01"}, {Key: "_id", Value: "01H0"}}

	b.ReportAllocs()
	for b.Loop() {
		_ = keysetFilter(sorts, keys)
	}
}

func BenchmarkSnapshot(b *testing.B) {
	// Paid once per hydrated document on a trackable model, so it is the
	// price of dirty tracking on a read.
	model := &tracked{Name: "Nana", Nickname: "NK"}
	model.ID = "01H0"

	b.ReportAllocs()
	for b.Loop() {
		if err := snapshot(model); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChangesClean(b *testing.B) {
	model := &tracked{Name: "Nana", Nickname: "NK"}
	model.ID = "01H0"
	if err := snapshot(model); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := Changes(model); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChangesDirty(b *testing.B) {
	model := &tracked{Name: "Nana", Nickname: "NK"}
	model.ID = "01H0"
	if err := snapshot(model); err != nil {
		b.Fatal(err)
	}
	model.Name = "Nana Kwesi"

	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := Changes(model); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRelationGrouping(b *testing.B) {
	// The in-memory half of eager loading: 500 related documents indexed by
	// the key their parents match on.
	raws := make([]bson.Raw, 500)
	for i := range raws {
		raw, err := bson.Marshal(bson.M{"_id": i, "user_id": i % 50, "total": i})
		if err != nil {
			b.Fatal(err)
		}
		raws[i] = raw
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = groupByKey(raws, "user_id")
	}
}

func BenchmarkRelationKeys(b *testing.B) {
	raws := make([]bson.Raw, 100)
	for i := range raws {
		raw, err := bson.Marshal(bson.M{"_id": i % 50})
		if err != nil {
			b.Fatal(err)
		}
		raws[i] = raw
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := documentKeys(raws, "_id"); err != nil {
			b.Fatal(err)
		}
	}
}
