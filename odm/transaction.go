package odm

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Transaction runs fn inside a MongoDB transaction, committing when it
// returns nil and aborting when it returns an error:
//
//	err := database.Transaction(ctx, func(ctx context.Context) error {
//		if err := users.Create(ctx, &user); err != nil {
//			return err
//		}
//		return accounts.Create(ctx, &account)
//	})
//
// The context handed to fn is the transaction. It carries the session, which
// is how the driver knows an operation belongs to the transaction, so every
// call inside must use *that* ctx — a call using the outer one runs outside
// the transaction and commits on its own, silently. That is also why there
// is no separate transactional collection type to construct: the collections
// you already have take part simply by being given this context.
//
// Reach the session itself with mongo.SessionFromContext(ctx) when you need
// it directly; nothing here wraps it.
//
// Two things to know, both MongoDB's rather than this package's:
//
//   - Transactions need a replica set or a sharded cluster. On a standalone
//     server the driver returns an error rather than running fn unprotected.
//   - fn can run more than once. WithTransaction, which this uses, is the
//     driver's implementation of MongoDB's own retry recommendation: it
//     retries the callback on a transient transaction error and retries the
//     commit when its outcome is unknown, giving up after about two minutes.
//     Keep fn idempotent, and keep anything that isn't a database write —
//     sending mail, charging a card — outside it.
func (db *DB) Transaction(ctx context.Context, fn func(ctx context.Context) error, opts ...options.Lister[options.TransactionOptions]) error {
	session, err := db.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		return nil, fn(ctx)
	}, opts...)
	return err
}
