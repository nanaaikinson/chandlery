// Package memory implements cache.Store on an in-process map. State is not
// shared across processes, and is lost on restart.
package memory

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nanaaikinson/chandlery/cache"
)

// Store is an in-process cache.Store backed by a map guarded by a mutex.
type Store struct {
	prefix string
	now    func() time.Time

	mu   sync.Mutex
	data map[string]entry
}

type entry struct {
	val       []byte
	expiresAt time.Time // zero means no expiry
}

var _ cache.Store = (*Store)(nil)

// Option configures a Store.
type Option func(*Store)

// WithClock overrides the Store's time source, used by tests to control
// TTL expiry deterministically instead of sleeping.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// New returns an empty Store. prefix gates Flush: an empty prefix makes
// Flush return cache.ErrNoPrefix.
func New(prefix string, opts ...Option) *Store {
	s := &Store{
		prefix: prefix,
		now:    time.Now,
		data:   make(map[string]entry),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.get(key)
	if !ok {
		return nil, false, nil
	}
	return copyBytes(e.val), true, nil
}

func (s *Store) Put(_ context.Context, key string, val []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = entry{val: copyBytes(val), expiresAt: s.expiryFor(ttl)}
	return nil
}

func (s *Store) Add(_ context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.get(key); ok {
		return false, nil
	}
	s.data[key] = entry{val: copyBytes(val), expiresAt: s.expiryFor(ttl)}
	return true, nil
}

func (s *Store) Forever(_ context.Context, key string, val []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = entry{val: copyBytes(val)}
	return nil
}

func (s *Store) Pull(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.get(key)
	if !ok {
		return nil, false, nil
	}
	delete(s.data, key)
	return copyBytes(e.val), true, nil
}

func (s *Store) Forget(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
	return nil
}

func (s *Store) Has(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.get(key)
	return ok, nil
}

func (s *Store) Increment(_ context.Context, key string, by int64) (int64, error) {
	return s.addTo(key, by)
}

// Decrement subtracts directly rather than negating by and delegating to
// addTo: by == math.MinInt64 has no representable positive counterpart, so
// -by would silently overflow back to math.MinInt64 itself (two's
// complement), turning a decrement into an increment.
func (s *Store) Decrement(_ context.Context, key string, by int64) (int64, error) {
	return s.subFrom(key, by)
}

func (s *Store) Flush(_ context.Context) error {
	if s.prefix == "" {
		return cache.ErrNoPrefix
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = make(map[string]entry)
	return nil
}

// get returns the live entry for key, evicting and reporting absent for
// one that has expired.
func (s *Store) get(key string) (entry, bool) {
	e, ok := s.data[key]
	if !ok {
		return entry{}, false
	}
	if !e.expiresAt.IsZero() && !s.now().Before(e.expiresAt) {
		delete(s.data, key)
		return entry{}, false
	}
	return e, true
}

func (s *Store) expiryFor(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return s.now().Add(ttl)
}

// addTo adds delta to key's integer value, preserving its existing ttl. An
// absent key is treated as 0 and the resulting key carries no expiry.
// Errors on overflow rather than silently wrapping, matching cache/redis's
// INCRBY (which Redis itself rejects server-side on overflow) — without
// this check the two backends would give different answers for the exact
// same call.
func (s *Store) addTo(key string, delta int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, expiresAt, err := s.currentInt(key)
	if err != nil {
		return 0, err
	}

	next := cur + delta
	if addOverflows(cur, delta, next) {
		return 0, fmt.Errorf("cache/memory: incrementing %q by %d would overflow int64", key, delta)
	}
	s.data[key] = entry{val: []byte(strconv.FormatInt(next, 10)), expiresAt: expiresAt}
	return next, nil
}

// subFrom subtracts delta from key's integer value. See addTo; kept as its
// own subtraction (rather than addTo(key, -delta)) so a delta of
// math.MinInt64 can't overflow merely by being negated.
func (s *Store) subFrom(key string, delta int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, expiresAt, err := s.currentInt(key)
	if err != nil {
		return 0, err
	}

	next := cur - delta
	if subOverflows(cur, delta, next) {
		return 0, fmt.Errorf("cache/memory: decrementing %q by %d would overflow int64", key, delta)
	}
	s.data[key] = entry{val: []byte(strconv.FormatInt(next, 10)), expiresAt: expiresAt}
	return next, nil
}

// currentInt returns key's current integer value (0 if absent) and its
// existing expiry (zero Time if absent), or an error if the stored value
// isn't a valid base-10 int64.
func (s *Store) currentInt(key string) (int64, time.Time, error) {
	e, ok := s.get(key)
	if !ok {
		return 0, time.Time{}, nil
	}
	cur, err := strconv.ParseInt(string(e.val), 10, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("cache/memory: value for %q is not an integer: %w", key, err)
	}
	return cur, e.expiresAt, nil
}

// addOverflows reports whether a+b, computed as c, overflowed int64 — the
// standard branch-free check: overflow happened iff the result's sign
// differs from both operands' shared sign.
func addOverflows(a, b, c int64) bool {
	return ((a ^ c) & (b ^ c)) < 0
}

// subOverflows reports whether a-b, computed as c, overflowed int64 —
// overflow happened iff a and b have different signs and the result's sign
// differs from a's.
func subOverflows(a, b, c int64) bool {
	return ((a ^ b) & (a ^ c)) < 0
}

// copyBytes returns an independent copy of b. Always non-nil (even for a
// nil or empty b) so a stored value's nil-ness can't depend on whether the
// caller happened to pass a nil vs. an empty slice — matching cache/redis,
// where a round trip through Redis has no way to preserve that distinction
// either (GET of an empty string always comes back as a non-nil, zero-length
// []byte).
func copyBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
