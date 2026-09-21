package odm

import (
	"bytes"
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// modelState is what a hydrated model remembers about the document it came
// from. It lives on the embedded IdentityModel, where it is unexported and
// therefore invisible to the BSON codec — internal bookkeeping is never
// written to the database.
type modelState struct {
	// exists records that this model came from, or has been written to, the
	// database. It is set by hydration and by a successful insert, never
	// inferred from a non-zero ID: a caller who assigns their own _id to a
	// brand new model would otherwise look like an update.
	exists bool
	// original is the model as it last agreed with the database, marshalled
	// the same way Save marshals it now. Snapshotting the *model* rather
	// than the document it was decoded from is what keeps a diff honest: a
	// field the document carries and T does not can never appear on either
	// side, so Save can never unset it.
	original bson.Raw
}

// stateful is implemented only by *IdentityModel, and so by anything
// embedding it or Model. A model that embeds neither has nowhere to keep
// this, which is exactly what makes it untracked.
type stateful interface {
	modelState() *modelState
}

func (m *IdentityModel) modelState() *modelState { return &m.state }

// stateOf reaches a model's bookkeeping, reporting false for a model type
// that keeps none.
func stateOf[T any](model *T) (*modelState, bool) {
	holder, ok := any(model).(stateful)
	if !ok {
		return nil, false
	}
	return holder.modelState(), true
}

// snapshot records the model as the database now holds it, so later changes
// have something to be measured against.
func snapshot[T any](model *T) error {
	state, ok := stateOf(model)
	if !ok {
		return nil
	}

	raw, err := bson.Marshal(model)
	if err != nil {
		return fmt.Errorf("odm: snapshotting model: %w", err)
	}
	state.exists = true
	state.original = raw
	return nil
}

// IsPersisted reports whether model came from the database — hydrated by a
// read, or written by Create or Save. A model you built yourself is not
// persisted, whatever its ID says, which is what Save uses to decide between
// an insert and an update.
//
// Named apart from Query.Exists on purpose: that one asks the database
// whether anything matches a filter, this one asks a model in hand where it
// came from, and one word for both questions would be a trap.
//
// Models that embed neither odm.Model nor odm.IdentityModel keep no state,
// so this is always false for them and Save always inserts.
func IsPersisted[T any](model *T) bool {
	state, ok := stateOf(model)
	return ok && state.exists
}

// Changes reports what Save would write: the persisted fields whose value
// differs from the snapshot, as a bson.M, plus the fields that have gone
// away and would be unset.
//
// The comparison is of BSON, not of Go memory, so it respects the model's
// own tags — a `bson:"-"` field (a loaded relation, say) is not a change,
// and an omitempty field falling to its zero value is a removal rather than
// an update. A model that doesn't exist yet reports no changes: everything
// about it is new.
func Changes[T any](model *T) (bson.M, []string, error) {
	_, set, unset, err := changesOf(model)
	return set, unset, err
}

// changesOf is Changes plus the marshalled model it compared, which Save
// needs anyway to find the document's _id — one marshal, not two.
func changesOf[T any](model *T) (bson.Raw, bson.M, []string, error) {
	state, ok := stateOf(model)
	if !ok || !state.exists {
		return nil, nil, nil, nil
	}

	current, err := bson.Marshal(model)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("odm: reading model: %w", err)
	}
	set, unset, err := diff(state.original, current)
	return current, set, unset, err
}

// IsDirty reports whether the model differs from its snapshot. Given field
// names it asks only about those, which is the common case in a hook:
// "did the email change?"
func IsDirty[T any](model *T, fields ...string) (bool, error) {
	set, unset, err := Changes(model)
	if err != nil {
		return false, err
	}

	if len(fields) == 0 {
		return len(set) > 0 || len(unset) > 0, nil
	}

	for _, field := range fields {
		if _, ok := set[field]; ok {
			return true, nil
		}
		for _, removed := range unset {
			if removed == field {
				return true, nil
			}
		}
	}
	return false, nil
}

// Original returns a field's value as it was when the model was last in
// agreement with the database — the "from" half of a change. The value comes
// back as a bson.RawValue, which keeps its exact type and can be unmarshalled
// into whatever the field is. Reports false for an untracked or new model,
// and for a field the snapshot doesn't carry.
func Original[T any](model *T, field string) (bson.RawValue, bool) {
	state, ok := stateOf(model)
	if !ok || !state.exists {
		return bson.RawValue{}, false
	}

	value, err := state.original.LookupErr(field)
	if err != nil {
		return bson.RawValue{}, false
	}
	return value, true
}

// diff compares two marshalled versions of one model, top-level field by
// top-level field.
//
// _id is skipped: it identifies the document being updated rather than being
// part of what is updated, and MongoDB rejects an update that touches it.
// Values are compared as raw bytes, which is exact and needs no knowledge of
// what any field holds.
func diff(original, current bson.Raw) (bson.M, []string, error) {
	currentElements, err := current.Elements()
	if err != nil {
		return nil, nil, fmt.Errorf("odm: reading model: %w", err)
	}
	originalElements, err := original.Elements()
	if err != nil {
		return nil, nil, fmt.Errorf("odm: reading snapshot: %w", err)
	}

	set := bson.M{}
	for _, element := range currentElements {
		key := element.Key()
		if key == "_id" {
			continue
		}

		value := element.Value()
		if was, err := original.LookupErr(key); err == nil && sameRawValue(was, value) {
			continue
		}

		var decoded any
		if err := value.Unmarshal(&decoded); err != nil {
			return nil, nil, fmt.Errorf("odm: reading field %q: %w", key, err)
		}
		set[key] = decoded
	}

	var unset []string
	for _, element := range originalElements {
		key := element.Key()
		if key == "_id" {
			continue
		}
		if _, err := current.LookupErr(key); err != nil {
			unset = append(unset, key)
		}
	}

	return set, unset, nil
}

func sameRawValue(a, b bson.RawValue) bool {
	return a.Type == b.Type && bytes.Equal(a.Value, b.Value)
}

// Save writes a model, inserting it when it is new and updating only what
// changed when it isn't:
//
//	user.Name = "Nana Kwesi"
//	err := users.Save(ctx, &user)
//
// Which of the two it does comes from whether the model exists — hydrated by
// a read, or written by an earlier Create or Save — never from whether its ID
// looks set. A model you built and gave an _id is still new.
//
// The update is a $set of the fields that differ from the snapshot and a
// $unset of the ones that have gone away, never a whole-document
// replacement: a field another writer changed in the meantime survives
// untouched unless this model changed it too. A model with nothing changed
// is not written at all, and Save returns nil without a round trip.
//
// Timestamps, hooks and observers all apply: BeforeUpdate and the Updating
// observers run first and may change what is written, since the update is
// recomputed after them; updated_at is refreshed, but only for a model that
// had something else to say. Afterwards the snapshot is refreshed, so the
// model is clean again.
//
// Models that embed neither odm.Model nor odm.IdentityModel keep no
// snapshot, so Save always inserts them — use Create and a query-level
// Update for those.
func (c *Collection[T]) Save(ctx context.Context, model *T) error {
	if model == nil {
		return ErrNilModel
	}
	if !IsPersisted(model) {
		return c.insert(ctx, model)
	}
	return c.update(ctx, model)
}

func (c *Collection[T]) update(ctx context.Context, model *T) error {
	// A first look, only to decide whether anything is happening at all: a
	// clean model should not wake a hook or an observer.
	dirty, err := IsDirty(model)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}

	if err := runBeforeUpdate(ctx, model); err != nil {
		return err
	}
	if err := notify(ctx, c.db, eventUpdating, model); err != nil {
		return err
	}

	// Recomputed, because the hook and the observers were free to change
	// the model and what they changed has to be written too.
	current, set, unset, err := changesOf(model)
	if err != nil {
		return err
	}
	if len(set) == 0 && len(unset) == 0 {
		return nil
	}

	if stamp, ok := any(model).(timestamped); ok {
		now := c.now()
		stamp.touchUpdatedAt(now)
		set[updatedAtField] = now
	}

	id, err := current.LookupErr("_id")
	if err != nil {
		return fmt.Errorf("odm: Save: the model has no _id to update by: %w", err)
	}

	update := bson.D{}
	if len(set) > 0 {
		update = append(update, bson.E{Key: "$set", Value: set})
	}
	if len(unset) > 0 {
		removed := make(bson.D, len(unset))
		for i, field := range unset {
			removed[i] = bson.E{Key: field, Value: ""}
		}
		update = append(update, bson.E{Key: "$unset", Value: removed})
	}

	if _, err := c.collection.UpdateOne(ctx, bson.M{"_id": id}, update); err != nil {
		return classify(err)
	}

	if err := runAfterUpdate(ctx, model); err != nil {
		return err
	}
	if err := notify(ctx, c.db, eventUpdated, model); err != nil {
		return err
	}

	// Last, so everything above still sees what changed.
	return snapshot(model)
}

// snapshotAll records a whole page of freshly read models, skipping the work
// entirely for a model type that keeps no state.
func snapshotAll[T any](models []T, trackable bool) error {
	if !trackable {
		return nil
	}
	for i := range models {
		if err := snapshot(&models[i]); err != nil {
			return err
		}
	}
	return nil
}
