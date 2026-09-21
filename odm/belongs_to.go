package odm

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// BelongsTo is the inverse of HasOne and HasMany: the key lives on this
// model, pointing at the document it belongs to — an order and its customer:
//
//	var OrderCustomer = odm.BelongsTo[Order, Customer]{
//		ForeignKey: "customer_id",
//		Attach:     func(o *Order, c *Customer) { o.Customer = c },
//	}
//
//	orders, err := orders.With(OrderCustomer).Get(ctx)
//
// Attach receives nil when the key is unset or points at nothing, so a
// dangling reference reads as absent rather than as a zero-valued document.
type BelongsTo[T, R any] struct {
	// ForeignKey is the field on *this* model holding the other document's
	// key, e.g. "customer_id". Required.
	ForeignKey string
	// OwnerKey is the field on the related model it points at. Defaults to
	// "_id".
	OwnerKey string
	// Attach receives the related document, or nil. Required.
	Attach func(model *T, related *R)
	// Nested are relations of the related model, loaded before Attach sees
	// it. Add them with With rather than setting this directly.
	Nested []Relation[R]
}

// With returns a copy of the relation that also loads relations of the
// related model. See HasMany.With.
func (r BelongsTo[T, R]) With(nested ...Relation[R]) BelongsTo[T, R] {
	r.Nested = cloneAppend(r.Nested, nested...)
	return r
}

func (r BelongsTo[T, R]) validate() error {
	if err := validateRelation("BelongsTo", r.ForeignKey, r.Attach == nil); err != nil {
		return err
	}
	return validateNested("BelongsTo", r.Nested)
}

func (r BelongsTo[T, R]) load(ctx context.Context, db *DB, parents []T, raws []bson.Raw) error {
	// The key is on this side, so the lookup runs the other way round from
	// HasOne: read the foreign key here, match the owner key there.
	keys, values, matched, err := relationKeys(raws, r.ForeignKey)
	if err != nil {
		return err
	}
	if !matched {
		attachNil(parents, r.Attach)
		return nil
	}

	owner := localKeyOr(r.OwnerKey)
	related, relatedRaws, err := relatedDocuments[R](ctx, db, owner, values)
	if err != nil {
		return err
	}
	if err := loadNested(ctx, db, r.Nested, related, relatedRaws); err != nil {
		return err
	}
	attachFirst(parents, keys, related, groupByKey(relatedRaws, owner), r.Attach)
	return nil
}
