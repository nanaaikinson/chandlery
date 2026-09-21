//go:build integration

package odm_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nanaaikinson/chandlery/odm"
)

// drain pages all the way through a query and returns the names it saw, in
// the order it saw them. It fails rather than loops forever if pagination
// never reports the end.
func drain(t *testing.T, query *odm.Query[user], size int64) []string {
	t.Helper()

	ctx := context.Background()
	var seen []string
	cursor := ""

	for pages := 0; ; pages++ {
		if pages > 50 {
			t.Fatal("pagination never reported the end")
		}

		page, err := query.CursorPaginate(ctx, odm.CursorPagination{Limit: size, Cursor: cursor})
		if err != nil {
			t.Fatalf("CursorPaginate() error = %v", err)
		}
		if int64(len(page.Data)) > size {
			t.Fatalf("page holds %d documents, want at most %d", len(page.Data), size)
		}
		seen = append(seen, names(page.Data)...)

		if !page.HasMore {
			if page.NextCursor != "" {
				t.Errorf("last page carries NextCursor = %q, want none", page.NextCursor)
			}
			return seen
		}
		if page.NextCursor == "" {
			t.Fatal("page reports HasMore with no NextCursor")
		}
		cursor = page.NextCursor
	}
}

// seedPaged writes count users named 000..N-1, all sharing one created_at,
// so every page boundary lands in the middle of a tie — the case a cursor
// built on created_at alone would skip or repeat rows across.
func seedPaged(t *testing.T, users *odm.Collection[user], count int) []string {
	t.Helper()

	shared := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expected := make([]string, count)

	for i := range count {
		name := string(rune('a' + i))
		model := &user{Name: name, Status: "active"}
		model.CreatedAt = shared
		seed(t, users, model)
		expected[i] = name
	}
	return expected
}

func TestCursorPaginate(t *testing.T) {
	t.Parallel()

	t.Run("walks every document exactly once across pages", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 7)

		// _id is a ULID assigned in insertion order, so ascending _id is
		// the order the documents were written in.
		got := drain(t, users.OrderBy("created_at", odm.Asc), 3)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("paged through %v, want %v — no duplicates, no gaps", got, expected)
		}
	})

	t.Run("descending walks the same documents in reverse", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 7)

		reversed := make([]string, len(expected))
		for i, name := range expected {
			reversed[len(expected)-1-i] = name
		}

		got := drain(t, users.OrderBy("created_at", odm.Desc), 3)
		if !reflect.DeepEqual(got, reversed) {
			t.Errorf("paged through %v, want %v", got, reversed)
		}
	})

	t.Run("an unsorted query paginates by _id", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 5)

		got := drain(t, users.Query(), 2)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("paged through %v, want %v", got, expected)
		}
	})

	t.Run("a page size dividing the total exactly still ends cleanly", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 6)

		got := drain(t, users.OrderBy("created_at", odm.Asc), 3)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("paged through %v, want %v", got, expected)
		}
	})

	t.Run("a single page reports no more", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seedPaged(t, users, 2)

		page, err := users.CursorPaginate(ctx, odm.CursorPagination{Limit: 10})
		if err != nil {
			t.Fatalf("CursorPaginate() error = %v", err)
		}
		if page.HasMore || page.NextCursor != "" {
			t.Errorf("HasMore/NextCursor = %t/%q, want false and empty", page.HasMore, page.NextCursor)
		}
		if len(page.Data) != 2 {
			t.Errorf("page holds %d documents, want 2", len(page.Data))
		}
	})

	t.Run("an empty result reports no more", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)

		page, err := users.CursorPaginate(ctx, odm.CursorPagination{Limit: 10})
		if err != nil {
			t.Fatalf("CursorPaginate() error = %v", err)
		}
		if page.HasMore || len(page.Data) != 0 {
			t.Errorf("HasMore/len = %t/%d, want false and 0", page.HasMore, len(page.Data))
		}
	})

	t.Run("filters apply to every page", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		seedPaged(t, users, 4)
		seed(t, users, &user{Name: "z", Status: "archived"})

		got := drain(t, users.Where("status", "active").OrderBy("created_at", odm.Asc), 2)
		if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
			t.Errorf("paged through %v, want %v", got, want)
		}
	})

	t.Run("paging twice from one base query is repeatable", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 5)

		base := users.Where("status", "active").OrderBy("created_at", odm.Asc)
		if got := drain(t, base, 2); !reflect.DeepEqual(got, expected) {
			t.Errorf("first walk = %v, want %v", got, expected)
		}
		if got := drain(t, base, 2); !reflect.DeepEqual(got, expected) {
			t.Errorf("second walk = %v, want %v — the base query was mutated", got, expected)
		}
	})

	t.Run("the query's own Limit and Skip are ignored", func(t *testing.T) {
		t.Parallel()

		users := newUsers(t)
		expected := seedPaged(t, users, 5)

		got := drain(t, users.OrderBy("created_at", odm.Asc).Limit(1).Skip(3), 2)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("paged through %v, want %v", got, expected)
		}
	})

	t.Run("a projection must keep the sort's fields", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		users := newUsers(t)
		seedPaged(t, users, 3)

		_, err := users.
			Select("name").
			OrderBy("age", odm.Asc).
			CursorPaginate(ctx, odm.CursorPagination{Limit: 2})
		if err == nil {
			t.Fatal("CursorPaginate() error = nil, want a complaint about the missing sort field")
		}
	})
}

func TestCursorPaginateSoftDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	notes := newNotes(t)
	seedNotes(t, notes,
		&note{Title: "a"},
		&note{Title: "b"},
		&note{Title: "gone"},
	)
	if _, err := notes.Where("title", "gone").Delete(ctx); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	var seen []string
	cursor := ""
	for {
		page, err := notes.CursorPaginate(ctx, odm.CursorPagination{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatalf("CursorPaginate() error = %v", err)
		}
		seen = append(seen, titles(page.Data)...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	if want := []string{"a", "b"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("paged through %v, want %v — soft-deleted documents stay hidden", seen, want)
	}
}
