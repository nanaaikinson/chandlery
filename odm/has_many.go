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
	// Nested are relations of the related model, loaded before Attach sees
	// them. Add them with With rather than setting this directly.
	Nested []Relation[R]
}

// With returns a copy of the relation that also loads relations of the
// related model:
//
//	users.With(UserOrders.With(OrderPayments)).Get(ctx)
//
// Each level is still one query, so this is three in total however many
// users and orders there are. The declaration itself is unchanged — With
// copies it, the same way a query builder copies a query — so one exported
// relation can be used both plain and nested.
func (r HasMany[T, R]) With(nested ...Relation[R]) HasMany[T, R] {
	r.Nested = cloneAppend(r.Nested, nested...)
	return r
}

func (r HasMany[T, R]) validate() error {
	if err := validateRelation("HasMany", r.ForeignKey, r.Attach == nil); err != nil {
		return err
	}
	return validateNested("HasMany", r.Nested)
}

func (r HasMany[T, R]) load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error {
	return loadMany(ctx, db, parents, raws, localKeyOr(r.LocalKey), r.ForeignKey, r.Nested, r.Attach)
}

// loadMany is the body both many-valued relations share. They differ only in
// which side holds which key: HasMany reads the parent's own key and matches
// it against a field on the related model, BelongsToMany reads a list off
// the parent and matches it against the related model's own key. Either way
// it is one query and an in-memory grouping.
func loadMany[T, R any](
	ctx context.Context,
	db *DB,
	parents []T,
	raws []bson.Raw,
	parentKey, relatedKey string,
	nested []Relation[R],
	attach func(*T, []R),
) error {
	keys, values, matched, err := relationKeys(raws, parentKey)
	if err != nil {
		return err
	}
	if !matched {
		attachEmpty(parents, attach)
		return nil
	}

	related, relatedRaws, err := relatedDocuments[R](ctx, db, relatedKey, values)
	if err != nil {
		return err
	}
	if err := loadNested(ctx, db, nested, related, relatedRaws); err != nil {
		return err
	}
	grouped := groupByKey(relatedRaws, relatedKey)

	for i := range parents {
		matches := matchesFor(keys[i], grouped)
		children := make([]R, 0, len(matches))
		for _, j := range matches {
			children = append(children, related[j])
		}
		attach(&parents[i], children)
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
