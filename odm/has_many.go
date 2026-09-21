package odm

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// HasMany links a parent to the many documents that point back at it — a
// user and their orders:
//
//	var UserOrders = odm.HasMany[User, Order]{
//		ForeignKey: "user_id",
//		Attach:     func(u *User, orders []Order) { u.Orders = orders },
//	}
//
//	users, err := users.With(UserOrders).Get(ctx)
//
// Attach is a plain function rather than a field name this package would
// have to find by reflection: the compiler checks it, and there is no string
// to get wrong. The field it writes to should be kept out of the document
// with `bson:"-"`, since a loaded relation is not part of the parent:
//
//	type User struct {
//		odm.Model `bson:",inline"`
//
//		Orders []Order `bson:"-"`
//	}
type HasMany[T, R any] struct {
	// ForeignKey is the field on the related model that holds the parent's
	// key, e.g. "user_id". Required.
	ForeignKey string
	// LocalKey is the field on the parent whose value the foreign key
	// matches. Defaults to "_id".
	LocalKey string
	// Attach receives the related documents for one parent, in the order
	// MongoDB returned them, and an empty slice when there are none.
	// Required.
	Attach func(parent *T, related []R)
}

func (r HasMany[T, R]) validate() error {
	return validateRelation("HasMany", r.ForeignKey, r.Attach == nil)
}

func (r HasMany[T, R]) load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error {
	keys, values, matched, err := relationKeys(raws, localKeyOr(r.LocalKey))
	if err != nil {
		return err
	}
	if !matched {
		attachEmpty(parents, r.Attach)
		return nil
	}

	related, relatedRaws, err := relatedDocuments[R](ctx, db, r.ForeignKey, values)
	if err != nil {
		return err
	}
	grouped := groupByKey(relatedRaws, r.ForeignKey)

	for i := range parents {
		matches := grouped[keys[i]]
		children := make([]R, 0, len(matches))
		for _, j := range matches {
			children = append(children, related[j])
		}
		r.Attach(&parents[i], children)
	}
	return nil
}

func attachEmpty[T, R any](parents []T, attach func(*T, []R)) {
	for i := range parents {
		attach(&parents[i], []R{})
	}
}

// localKeyOr defaults a parent-side key to _id, which is what almost every
// relation matches on.
func localKeyOr(key string) string {
	if key == "" {
		return "_id"
	}
	return key
}

func validateRelation(kind, foreignKey string, missingAttach bool) error {
	if foreignKey == "" {
		return fmt.Errorf("%s: ForeignKey is empty", kind)
	}
	if missingAttach {
		return fmt.Errorf("%s(%q): Attach is nil — the relation has nowhere to put what it loads", kind, foreignKey)
	}
	return nil
}
