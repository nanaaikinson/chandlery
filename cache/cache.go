package cache

import (
	"context"
	"errors"
	"time"
)

// ErrNoPrefix is returned by Flush on a store with no key prefix, where a
// wildcard delete would reach keys this package doesn't own.
var ErrNoPrefix = errors.New("cache: refusing to flush an unprefixed store")

// Store is the driver contract. All keys are relative — a driver applies
// its own prefix, so callers never spell one out.
type Store interface {
	// Get returns the raw value and whether the key was present. A missing
	// key is (nil, false, nil), not an error.
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Put writes val, expiring after ttl. A ttl <= 0 means no expiry.
	Put(ctx context.Context, key string, val []byte, ttl time.Duration) error

	// Add writes val only if key is absent, reporting whether it wrote.
	Add(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error)

	// Forever writes val with no expiry.
	Forever(ctx context.Context, key string, val []byte) error

	// Pull returns the value and removes the key, atomically.
	Pull(ctx context.Context, key string) ([]byte, bool, error)

	// Forget removes the key. Removing an absent key is not an error.
	Forget(ctx context.Context, key string) error

	// Has reports whether the key is present.
	Has(ctx context.Context, key string) (bool, error)

	// Increment adds by to the key's integer value, returning the result.
	// An absent key is treated as 0. Errors rather than silently wrapping
	// if the result would overflow int64.
	Increment(ctx context.Context, key string, by int64) (int64, error)

	// Decrement subtracts by from the key's integer value. Same overflow
	// behavior as Increment.
	Decrement(ctx context.Context, key string, by int64) (int64, error)

	// Flush removes every key this store owns, and only those. Backends
	// differ in what "atomically" means here: cache/memory's Flush is a
	// single atomic swap, but cache/redis's is a SCAN+DEL over several round
	// trips, so a Put racing a Flush against the same store can observe the
	// key surviving it. Don't rely on Flush as a barrier against concurrent
	// writers.
	Flush(ctx context.Context) error
}
