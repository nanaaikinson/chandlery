package odm

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// BelongsToMany links a model to many others through a list of their ids
// kept on this one — a user and the roles they hold:
//
//	type User struct {
//		odm.Model `bson:",inline"`
//
//		RoleIDs []string `bson:"role_ids"`
//		Roles   []Role   `bson:"-"`
//	}
//
//	var UserRoles = odm.BelongsToMany[User, Role]{
//		LocalKey: "role_ids",
//		Attach:   func(u *User, roles []Role) { u.Roles = roles },
//	}
//
// This is MongoDB's usual answer to a many-to-many, and there is no join
// collection in it: a document can hold a list where a relational row cannot,
// so the list is the relationship. Loading it is still one query — every
// id from every parent goes into a single $in.
//
// The other direction needs no separate declaration. Where the list lives on
// the related model rather than this one, that is a HasMany whose foreign key
// happens to hold an array, and HasMany reads it the same way:
//
//	var RoleUsers = odm.HasMany[Role, User]{
//		ForeignKey: "role_ids",
//		Attach:     func(r *Role, users []User) { r.Users = users },
//	}
//
// A join collection carrying its own fields — when the link was granted, by
// whom — is a different shape, and a model of its own with two BelongsTo
// relations expresses it without this package needing to know.
type BelongsToMany[T, R any] struct {
	// LocalKey is the field on this model holding the related ids, e.g.
	// "role_ids". Required, and it should hold an array — a scalar works
	// too, which is then just a BelongsTo spelt differently.
	LocalKey string
	// OwnerKey is the field on the related model those ids point at.
	// Defaults to "_id".
	OwnerKey string
	// Attach receives the related documents for one parent, in the order
	// MongoDB returned them, and an empty slice when there are none.
	// Required.
	Attach func(parent *T, related []R)
	// Nested are relations of the related model, loaded before Attach sees
	// them. Add them with With rather than setting this directly.
	Nested []Relation[R]
}

// With returns a copy of the relation that also loads relations of the
// related model. See HasMany.With.
func (r BelongsToMany[T, R]) With(nested ...Relation[R]) BelongsToMany[T, R] {
	r.Nested = cloneAppend(r.Nested, nested...)
	return r
}

func (r BelongsToMany[T, R]) validate() error {
	// LocalKey has no useful default here: the whole point is that the ids
	// live in a field of their own, not in _id.
	if r.LocalKey == "" {
		return fmt.Errorf("BelongsToMany: LocalKey is empty — name the field holding the related ids")
	}
	if r.Attach == nil {
		return fmt.Errorf("BelongsToMany(%q): Attach is nil — the relation has nowhere to put what it loads", r.LocalKey)
	}
	return validateNested("BelongsToMany", r.Nested)
}

func (r BelongsToMany[T, R]) load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error {
	return loadMany(ctx, db, parents, raws, r.LocalKey, localKeyOr(r.OwnerKey), r.Nested, r.Attach)
}
