package odm

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// HasOne is HasMany where the parent expects at most one match — a user and
// their profile:
//
//	var UserProfile = odm.HasOne[User, Profile]{
//		ForeignKey: "user_id",
//		Attach:     func(u *User, p *Profile) { u.Profile = p },
//	}
//
// Attach receives nil when there is no match, so an absent relation is
// distinguishable from a zero-valued one. Nothing enforces the "one" — if
// the data holds several, the first MongoDB returns wins, and the rest are
// ignored rather than reported. Use HasMany when that matters.
type HasOne[T, R any] struct {
	// ForeignKey is the field on the related model holding the parent's
	// key. Required.
	ForeignKey string
	// LocalKey is the field on the parent it matches. Defaults to "_id".
	LocalKey string
	// Attach receives the related document, or nil. Required.
	Attach func(parent *T, related *R)
	// Nested are relations of the related model, loaded before Attach sees
	// it. Add them with With rather than setting this directly.
	Nested []Relation[R]
}

// With returns a copy of the relation that also loads relations of the
// related model. See HasMany.With.
func (r HasOne[T, R]) With(nested ...Relation[R]) HasOne[T, R] {
	r.Nested = cloneAppend(r.Nested, nested...)
	return r
}

func (r HasOne[T, R]) validate() error {
	if err := validateRelation("HasOne", r.ForeignKey, r.Attach == nil); err != nil {
		return err
	}
	return validateNested("HasOne", r.Nested)
}

func (r HasOne[T, R]) load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error {
	keys, values, matched, err := relationKeys(raws, localKeyOr(r.LocalKey))
	if err != nil {
		return err
	}
	if !matched {
		attachNil(parents, r.Attach)
		return nil
	}

	related, relatedRaws, err := relatedDocuments[R](ctx, db, r.ForeignKey, values)
	if err != nil {
		return err
	}
	if err := loadNested(ctx, db, r.Nested, related, relatedRaws); err != nil {
		return err
	}
	attachFirst(parents, keys, related, groupByKey(relatedRaws, r.ForeignKey), r.Attach)
	return nil
}

// attachFirst hands each parent the first document matching its key, or nil.
func attachFirst[T, R any](parents []T, keys []keyID, related []R, grouped map[keyID][]int, attach func(*T, *R)) {
	for i := range parents {
		matches := grouped[keys[i]]
		if len(matches) == 0 {
			attach(&parents[i], nil)
			continue
		}
		attach(&parents[i], &related[matches[0]])
	}
}

func attachNil[T, R any](parents []T, attach func(*T, *R)) {
	for i := range parents {
		attach(&parents[i], nil)
	}
}
