package odm

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// CursorPagination asks for one page.
type CursorPagination struct {
	// Limit is how many documents the page holds. Required, and positive.
	Limit int64
	// Cursor is the NextCursor of the page before, or empty for the first.
	Cursor string
}

// CursorPage is one page of results plus what it takes to ask for the next.
// The JSON tags are there because this is usually what an API hands back.
type CursorPage[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// CursorPaginate fetches one page, seeking straight to it rather than
// counting past the pages before it the way Skip does:
//
//	page, err := users.
//		Where("business_id", businessID).
//		OrderBy("created_at", odm.Desc).
//		CursorPaginate(ctx, odm.CursorPagination{Limit: 20, Cursor: cursor})
//
// Hand page.NextCursor back as Cursor for the page after, while
// page.HasMore says there is one. The cursor is opaque: it encodes the sort
// it was produced under and the last document's sort values, and is only
// valid against a query sorting the same way.
//
// The query's own sort drives the paging, with _id appended as a tie-breaker
// unless it is already in there — documents sharing a created_at have no
// order between them otherwise, and a cursor over them would skip or repeat
// rows. An unsorted query paginates by _id ascending.
//
// Limit and Skip on the query are ignored: a page's size is
// CursorPagination.Limit, and seeking is what replaces skipping. Everything
// else — filters, projection, the soft-delete scope — applies as usual, but
// a projection has to keep every field the sort uses, since the next
// cursor is read out of the documents returned.
func (q *Query[T]) CursorPaginate(ctx context.Context, page CursorPagination) (CursorPage[T], error) {
	var empty CursorPage[T]
	if q.err != nil {
		return empty, q.err
	}
	if page.Limit <= 0 {
		return empty, fmt.Errorf("%w: CursorPaginate: Limit is %d, want a positive page size", ErrInvalidQuery, page.Limit)
	}

	sorts := paginationSorts(q.sorts)

	seek, err := q.seekQuery(sorts, page.Cursor)
	if err != nil {
		return empty, err
	}

	// One more than asked for, so the extra document answers HasMore
	// without a second query.
	opts := seek.findOptions().SetSort(sorts).SetLimit(page.Limit + 1).SetSkip(0)

	cursor, err := q.collection.collection.Find(ctx, seek.filter(), opts)
	if err != nil {
		return empty, err
	}
	defer cursor.Close(ctx)

	models := make([]T, 0, page.Limit)
	var raws []bson.Raw
	var last bson.Raw

	for cursor.Next(ctx) {
		if int64(len(models)) == page.Limit {
			// The extra document: its only job was to exist.
			return q.finishPage(ctx, models, raws, last, sorts, true)
		}

		var model T
		if err := cursor.Decode(&model); err != nil {
			return empty, err
		}
		models = append(models, model)

		// Cursor.Current is reused between iterations, so keep a copy. The
		// last one mints the next cursor; the rest are only needed when a
		// relation has to read keys off them.
		document := bson.Raw(append([]byte(nil), cursor.Current...))
		last = document
		if len(q.with) > 0 {
			raws = append(raws, document)
		}
	}
	if err := cursor.Err(); err != nil {
		return empty, err
	}

	return q.finishPage(ctx, models, raws, last, sorts, false)
}

// seekQuery adds the "everything after the cursor" condition, returning the
// query untouched when there is no cursor to seek past — a first page. Like
// every other builder step it copies rather than mutates, so paging twice
// from one base query is safe.
func (q *Query[T]) seekQuery(sorts bson.D, cursor string) (*Query[T], error) {
	if cursor == "" {
		return q, nil
	}

	keys, err := decodeCursor(cursor, sorts)
	if err != nil {
		return nil, err
	}
	return q.withFilter(keysetFilter(sorts, keys)), nil
}

// finishPage loads any eager relations and mints the cursor for whatever the
// page ended on. A page that ran out of documents gets no cursor: there is
// nothing after it to seek to.
func (q *Query[T]) finishPage(ctx context.Context, models []T, raws []bson.Raw, last bson.Raw, sorts bson.D, hasMore bool) (CursorPage[T], error) {
	if err := snapshotAll(models, q.collection.meta.trackable); err != nil {
		return CursorPage[T]{}, err
	}
	if err := q.loadRelations(ctx, models, raws); err != nil {
		return CursorPage[T]{}, err
	}

	page := CursorPage[T]{Data: models, HasMore: hasMore}
	if !hasMore || len(models) == 0 {
		return page, nil
	}

	keys, err := cursorKeys(last, sorts)
	if err != nil {
		return CursorPage[T]{}, err
	}

	next, err := encodeCursor(sorts, keys)
	if err != nil {
		return CursorPage[T]{}, err
	}
	page.NextCursor = next
	return page, nil
}
