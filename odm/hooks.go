package odm

import (
	"context"
	"fmt"
)

// BeforeCreate runs on a model just before Create inserts it, and aborts the
// insert if it returns an error. Implement it on the pointer receiver, so
// the hook can change what gets written:
//
//	func (u *User) BeforeCreate(ctx context.Context) error {
//		u.Email = strings.ToLower(strings.TrimSpace(u.Email))
//		return nil
//	}
//
// It runs before the ULID and timestamps are assigned, so a hook that sets
// its own ID or CreatedAt wins.
type BeforeCreate interface {
	BeforeCreate(ctx context.Context) error
}

// AfterCreate runs once Create's insert has succeeded. An error from it is
// returned to the caller, but the document is already written — this hook
// cannot undo the insert, and nothing here rolls it back. Use it for work
// that follows a create, not for validation, which belongs in BeforeCreate.
type AfterCreate interface {
	AfterCreate(ctx context.Context) error
}

// BeforeUpdate runs on a model just before Save writes its changes, and
// aborts the write if it returns an error. It can change the model, and what
// it changes is written: the update is recomputed after the hook has run.
//
// It fires only for Save, which is the only operation holding a model —
// a query-level Update touches documents it never hydrates.
type BeforeUpdate interface {
	BeforeUpdate(ctx context.Context) error
}

// AfterUpdate runs once Save's write has succeeded. The model's snapshot has
// not been refreshed yet, so odm.Changes still reports what was written. An
// error from it reaches the caller but undoes nothing.
type AfterUpdate interface {
	AfterUpdate(ctx context.Context) error
}

func runBeforeUpdate(ctx context.Context, model any) error {
	hook, ok := model.(BeforeUpdate)
	if !ok {
		return nil
	}
	if err := hook.BeforeUpdate(ctx); err != nil {
		return fmt.Errorf("odm: BeforeUpdate: %w", err)
	}
	return nil
}

func runAfterUpdate(ctx context.Context, model any) error {
	hook, ok := model.(AfterUpdate)
	if !ok {
		return nil
	}
	if err := hook.AfterUpdate(ctx); err != nil {
		return fmt.Errorf("odm: AfterUpdate: %w", err)
	}
	return nil
}

// There is no BeforeDelete or AfterDelete, and the update pair above fires
// only for Save.
//
// A hook needs a model, and the query-level operations — Update, Delete and
// their variants — never hydrate one: they act on every document the filter
// matches, straight on the server. Handing those a hook would mean either
// reading every matching document first, turning one round trip into three,
// or calling the hook on a model whose fields don't reflect what was
// written. Save is the only operation that both changes a document and holds
// the model for it, which is why it is the only one with update hooks, and
// why delete has none at all: nothing in this package deletes a model
// instance.
//
// Every dispatch below is a plain interface assertion — no reflection.
func runBeforeCreate(ctx context.Context, model any) error {
	hook, ok := model.(BeforeCreate)
	if !ok {
		return nil
	}
	if err := hook.BeforeCreate(ctx); err != nil {
		return fmt.Errorf("odm: BeforeCreate: %w", err)
	}
	return nil
}

func runAfterCreate(ctx context.Context, model any) error {
	hook, ok := model.(AfterCreate)
	if !ok {
		return nil
	}
	if err := hook.AfterCreate(ctx); err != nil {
		return fmt.Errorf("odm: AfterCreate: %w", err)
	}
	return nil
}
