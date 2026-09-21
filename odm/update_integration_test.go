//go:build integration

package odm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/nanaaikinson/chandlery/odm"
)

func TestUpdateOperations(t *testing.T) {
	t.Parallel()

	t.Run("applies $set and $inc together", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		target := &user{Name: "Nana", LoginCount: 2}
		seed(t, users, target, &user{Name: "Other", LoginCount: 99})

		result, err := users.
			Where("_id", target.ID).
			Set("name", "Nana Kwesi").
			Inc("login_count", 1).
			Update(ctx)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			t.Errorf("Update() matched/modified = %d/%d, want 1/1", result.MatchedCount, result.ModifiedCount)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.Name != "Nana Kwesi" || got.LoginCount != 3 {
			t.Errorf("Find() = name %q, login_count %d, want %q, 3", got.Name, got.LoginCount, "Nana Kwesi")
		}

		untouched, err := users.Where("name", "Other").First(ctx)
		if err != nil {
			t.Fatalf("First() error = %v", err)
		}
		if untouched.LoginCount != 99 {
			t.Errorf("unmatched document's login_count = %d, want 99", untouched.LoginCount)
		}
	})

	t.Run("Update touches every match", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "pending"},
			&user{Name: "b", Status: "pending"},
			&user{Name: "c", Status: "active"},
		)

		result, err := users.Where("status", "pending").Set("status", "active").Update(ctx)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if result.ModifiedCount != 2 {
			t.Errorf("Update() modified = %d, want 2", result.ModifiedCount)
		}

		count, err := users.Where("status", "active").Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 3 {
			t.Errorf("Count() = %d, want 3", count)
		}
	})

	t.Run("UpdateOne touches exactly one of several matches", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "pending"},
			&user{Name: "b", Status: "pending"},
		)

		result, err := users.
			Where("status", "pending").
			Set("status", "active").
			UpdateOne(ctx)
		if err != nil {
			t.Fatalf("UpdateOne() error = %v", err)
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			t.Errorf("UpdateOne() matched/modified = %d/%d, want 1/1", result.MatchedCount, result.ModifiedCount)
		}

		// Which one is the server's choice — OrderBy deliberately doesn't
		// steer it — so only the count is asserted.
		pending, err := users.Where("status", "pending").Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if pending != 1 {
			t.Errorf("remaining pending = %d, want 1", pending)
		}
	})

	t.Run("Unset removes a field rather than nulling it", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		target := &user{Name: "Nana", Nickname: "NK"}
		seed(t, users, target)

		if _, err := users.Where("_id", target.ID).Unset("nickname").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		var raw bson.M
		if err := users.Raw().FindOne(ctx, bson.M{"_id": target.ID}).Decode(&raw); err != nil {
			t.Fatalf("FindOne() error = %v", err)
		}
		if _, ok := raw["nickname"]; ok {
			t.Errorf("document still carries nickname = %v, want the field gone", raw["nickname"])
		}
	})

	t.Run("Decrement subtracts", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		target := &user{Name: "Nana", LoginCount: 10}
		seed(t, users, target)

		if _, err := users.Where("_id", target.ID).Decrement("login_count", 4).Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.LoginCount != 6 {
			t.Errorf("login_count = %d, want 6", got.LoginCount)
		}
	})

	t.Run("reports a filter that matched nothing without erroring", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "Nana"})

		result, err := users.Where("name", "Nobody").Set("name", "x").Update(ctx)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if result.MatchedCount != 0 || result.ModifiedCount != 0 {
			t.Errorf("Update() matched/modified = %d/%d, want 0/0", result.MatchedCount, result.ModifiedCount)
		}
	})
}

func TestArrayUpdates(t *testing.T) {
	t.Parallel()

	t.Run("Push appends, AddToSet deduplicates", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		target := &user{Name: "Nana", Tags: []string{"go"}, Roles: []string{"admin"}}
		seed(t, users, target)

		if _, err := users.Where("_id", target.ID).Push("tags", "mongo").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		// Pushing the same value again appends a duplicate; AddToSet won't.
		if _, err := users.Where("_id", target.ID).Push("tags", "mongo").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if _, err := users.Where("_id", target.ID).AddToSet("roles", "admin").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if _, err := users.Where("_id", target.ID).AddToSet("roles", "owner").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if want := []string{"go", "mongo", "mongo"}; !reflect.DeepEqual(got.Tags, want) {
			t.Errorf("tags = %v, want %v", got.Tags, want)
		}
		if want := []string{"admin", "owner"}; !reflect.DeepEqual(got.Roles, want) {
			t.Errorf("roles = %v, want %v", got.Roles, want)
		}
	})

	t.Run("Pull removes every matching element", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		target := &user{Name: "Nana", Tags: []string{"go", "php", "mongo", "php"}}
		seed(t, users, target)

		if _, err := users.Where("_id", target.ID).Pull("tags", "php").Update(ctx); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if want := []string{"go", "mongo"}; !reflect.DeepEqual(got.Tags, want) {
			t.Errorf("tags = %v, want %v", got.Tags, want)
		}
	})
}

func TestUpdateRawOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	users := newUsers(t)
	target := &user{Name: "Nana", LoginCount: 1, Tags: []string{"go"}}
	seed(t, users, target)

	t.Run("applies a native update document", func(t *testing.T) {
		t.Parallel()

		result, err := users.Where("_id", target.ID).UpdateRaw(ctx, bson.M{
			"$set": bson.M{"name": "Nana Kwesi"},
			"$inc": bson.M{"login_count": 5},
		})
		if err != nil {
			t.Fatalf("UpdateRaw() error = %v", err)
		}
		if result.ModifiedCount != 1 {
			t.Errorf("UpdateRaw() modified = %d, want 1", result.ModifiedCount)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if got.Name != "Nana Kwesi" || got.LoginCount != 6 {
			t.Errorf("Find() = name %q, login_count %d, want %q, 6", got.Name, got.LoginCount, "Nana Kwesi")
		}
	})

	t.Run("reaches operators the builder doesn't stage, like $each", func(t *testing.T) {
		t.Parallel()

		if _, err := users.Where("_id", target.ID).UpdateRaw(ctx, bson.M{
			"$push": bson.M{"tags": bson.M{"$each": bson.A{"mongo", "odm"}}},
		}); err != nil {
			t.Fatalf("UpdateRaw() error = %v", err)
		}

		got, err := users.Find(ctx, target.ID)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if want := []string{"go", "mongo", "odm"}; !reflect.DeepEqual(got.Tags, want) {
			t.Errorf("tags = %v, want %v", got.Tags, want)
		}
	})
}

func TestDeleteOperations(t *testing.T) {
	t.Parallel()

	t.Run("Delete removes every match, permanently", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		gone := &user{Name: "a", Status: "inactive"}
		seed(t, users,
			gone,
			&user{Name: "b", Status: "inactive"},
			&user{Name: "c", Status: "active"},
		)

		result, err := users.Where("status", "inactive").Delete(ctx)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if result.DeletedCount != 2 {
			t.Errorf("Delete() deleted = %d, want 2", result.DeletedCount)
		}

		if _, err := users.Find(ctx, gone.ID); !errors.Is(err, odm.ErrModelNotFound) {
			t.Errorf("Find() error = %v, want ErrModelNotFound after the delete", err)
		}

		remaining, err := users.Get(ctx)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if want := []string{"c"}; !reflect.DeepEqual(names(remaining), want) {
			t.Errorf("Get() = %v, want %v", names(remaining), want)
		}
	})

	t.Run("DeleteOne removes at most one", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users,
			&user{Name: "a", Status: "inactive"},
			&user{Name: "b", Status: "inactive"},
		)

		result, err := users.Where("status", "inactive").DeleteOne(ctx)
		if err != nil {
			t.Fatalf("DeleteOne() error = %v", err)
		}
		if result.DeletedCount != 1 {
			t.Errorf("DeleteOne() deleted = %d, want 1", result.DeletedCount)
		}

		count, err := users.Count(ctx)
		if err != nil {
			t.Fatalf("Count() error = %v", err)
		}
		if count != 1 {
			t.Errorf("Count() = %d, want 1", count)
		}
	})

	t.Run("deleting nothing is not an error", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seed(t, users, &user{Name: "a", Status: "active"})

		result, err := users.Where("status", "archived").Delete(ctx)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if result.DeletedCount != 0 {
			t.Errorf("Delete() deleted = %d, want 0", result.DeletedCount)
		}
	})
}
