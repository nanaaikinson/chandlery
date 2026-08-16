// Package task is the domain shared by both examples: the Task model, its
// create/patch request shapes and validation schemas, and the paginated
// list response. Both examples/fiber and examples/nethttp import this
// unchanged — everything that differs between them is the HTTP framework
// adapter around it, not the domain itself.
package task

import (
	"github.com/Oudwins/zog"
	"github.com/uptrace/bun"

	"github.com/nanaaikinson/chandlery/db"
)

// Task is the example domain model, persisted via db.Repository[*Task] and
// (for reads) cached ahead of Postgres via a cache.Store.
type Task struct {
	bun.BaseModel `bun:"table:tasks,alias:t"`
	db.Model

	Title string `bun:"title,notnull" json:"title"`
	Done  bool   `bun:"done,notnull,default:false" json:"done"`
}

// TableSchema creates the tasks table if it isn't already there. A real
// service would run this through a migration tool instead; inlining it here
// keeps the example runnable against a bare Postgres with no extra tooling.
const TableSchema = `
	create table if not exists tasks (
		id text primary key,
		title text not null,
		done boolean not null default false,
		created_at timestamptz not null default current_timestamp,
		updated_at timestamptz not null default current_timestamp
	);
`

// CreateRequest is the POST /tasks payload: a full task, so every field is
// required. validator/fiber's and validator/nethttp's ValidateRequest[T]
// decode and validate it against CreateSchema before a handler ever sees it.
type CreateRequest struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// CreateSchema's keys are matched against CreateRequest's field names
// case-insensitively (zog capitalizes the key's first letter and looks up
// that field) — "title" -> Title, "done" -> Done. No struct tags needed.
var CreateSchema = zog.Struct(zog.Shape{
	"title": zog.String().Required().Trim().Min(1).Max(200),
	"done":  zog.Bool(),
})

// PatchRequest is the PATCH /tasks/{id} payload: a true partial update, so
// every field is a pointer — nil means "leave this field alone," distinct
// from its zero value. A plain `Done bool` here couldn't tell an omitted
// "done" apart from an explicit "done: false", which would silently reset a
// completed task back to not-done on any title-only patch.
type PatchRequest struct {
	Title *string `json:"title"`
	Done  *bool   `json:"done"`
}

// PatchSchema validates each field only when present: zog.Ptr's wrapped
// schema simply isn't run against a nil pointer (and, with no .NotNil()
// call here, that's not itself a validation failure), but a *provided*
// title still has to pass the same Trim/Min/Max Title always has.
var PatchSchema = zog.Struct(zog.Shape{
	"title": zog.Ptr(zog.String().Trim().Min(1).Max(200)),
	"done":  zog.Ptr(zog.Bool()),
})

// List is the paginated response a task list endpoint returns.
type List struct {
	Items  []*Task `json:"items"`
	Count  int     `json:"count"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}
