package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestConformance runs the shared Store behavioral suite against a Store
// built by factory, so every backend (memory, redis, ...) is held to
// identical guarantees. Each subtest calls factory fresh, so subtests are
// independent and safe to run in parallel.
//
// The Store factory returns must use a non-empty prefix, so the subtest
// covering a successful Flush passes. Call TestFlushRequiresPrefix
// separately with an empty-prefix Store to cover that case.
//
// advance moves the Store's clock forward, so TTL expiry is testable
// without sleeping.
func TestConformance(t *testing.T, factory func() (Store, func(time.Duration))) {
	t.Helper()
	ctx := context.Background()

	t.Run("get missing key", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		val, ok, err := s.Get(ctx, "missing")
		if err != nil || ok || val != nil {
			t.Fatalf("Get(missing) = (%v, %v, %v), want (nil, false, nil)", val, ok, err)
		}
	})

	t.Run("put then get", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		if err := s.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		val, ok, err := s.Get(ctx, "k")
		if err != nil || !ok || string(val) != "v" {
			t.Fatalf("Get(k) = (%s, %v, %v), want (v, true, nil)", val, ok, err)
		}
	})

	t.Run("put with non-positive ttl never expires", func(t *testing.T) {
		t.Parallel()
		s, advance := factory()
		if err := s.Put(ctx, "k", []byte("v"), 0); err != nil {
			t.Fatalf("Put: %v", err)
		}
		advance(24 * time.Hour)
		_, ok, err := s.Get(ctx, "k")
		if err != nil || !ok {
			t.Fatalf("Get(k) after advance = (_, %v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("put expires after ttl", func(t *testing.T) {
		t.Parallel()
		s, advance := factory()
		if err := s.Put(ctx, "k", []byte("v"), time.Second); err != nil {
			t.Fatalf("Put: %v", err)
		}
		advance(2 * time.Second)
		val, ok, err := s.Get(ctx, "k")
		if err != nil || ok || val != nil {
			t.Fatalf("Get(k) after expiry = (%v, %v, %v), want (nil, false, nil)", val, ok, err)
		}
	})

	t.Run("add only writes when absent", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		wrote, err := s.Add(ctx, "k", []byte("first"), time.Minute)
		if err != nil || !wrote {
			t.Fatalf("Add(first) = (%v, %v), want (true, nil)", wrote, err)
		}
		wrote, err = s.Add(ctx, "k", []byte("second"), time.Minute)
		if err != nil || wrote {
			t.Fatalf("Add(second) = (%v, %v), want (false, nil)", wrote, err)
		}
		val, _, _ := s.Get(ctx, "k")
		if string(val) != "first" {
			t.Fatalf("Get(k) = %s, want first", val)
		}
	})

	t.Run("add writes again once the previous value expired", func(t *testing.T) {
		t.Parallel()
		s, advance := factory()
		if _, err := s.Add(ctx, "k", []byte("first"), time.Second); err != nil {
			t.Fatalf("Add(first): %v", err)
		}
		advance(2 * time.Second)
		wrote, err := s.Add(ctx, "k", []byte("second"), time.Minute)
		if err != nil || !wrote {
			t.Fatalf("Add(second) = (%v, %v), want (true, nil)", wrote, err)
		}
	})

	t.Run("forever never expires", func(t *testing.T) {
		t.Parallel()
		s, advance := factory()
		if err := s.Forever(ctx, "k", []byte("v")); err != nil {
			t.Fatalf("Forever: %v", err)
		}
		advance(365 * 24 * time.Hour)
		_, ok, err := s.Get(ctx, "k")
		if err != nil || !ok {
			t.Fatalf("Get(k) after advance = (_, %v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("pull removes the key", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		if err := s.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		val, ok, err := s.Pull(ctx, "k")
		if err != nil || !ok || string(val) != "v" {
			t.Fatalf("Pull(k) = (%s, %v, %v), want (v, true, nil)", val, ok, err)
		}
		_, ok, err = s.Get(ctx, "k")
		if err != nil || ok {
			t.Fatalf("Get(k) after Pull = (_, %v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("pull missing key", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		val, ok, err := s.Pull(ctx, "missing")
		if err != nil || ok || val != nil {
			t.Fatalf("Pull(missing) = (%v, %v, %v), want (nil, false, nil)", val, ok, err)
		}
	})

	t.Run("forget is idempotent", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		if err := s.Forget(ctx, "missing"); err != nil {
			t.Fatalf("Forget(missing): %v", err)
		}
		if err := s.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Forget(ctx, "k"); err != nil {
			t.Fatalf("Forget(k): %v", err)
		}
		_, ok, err := s.Get(ctx, "k")
		if err != nil || ok {
			t.Fatalf("Get(k) after Forget = (_, %v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("has reports presence", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		ok, err := s.Has(ctx, "k")
		if err != nil || ok {
			t.Fatalf("Has(k) = (%v, %v), want (false, nil)", ok, err)
		}
		if err := s.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		ok, err = s.Has(ctx, "k")
		if err != nil || !ok {
			t.Fatalf("Has(k) = (%v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("increment from absent starts at zero", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		got, err := s.Increment(ctx, "counter", 5)
		if err != nil || got != 5 {
			t.Fatalf("Increment = (%d, %v), want (5, nil)", got, err)
		}
		got, err = s.Increment(ctx, "counter", 3)
		if err != nil || got != 8 {
			t.Fatalf("Increment = (%d, %v), want (8, nil)", got, err)
		}
	})

	t.Run("decrement from absent starts at zero", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		got, err := s.Decrement(ctx, "counter", 5)
		if err != nil || got != -5 {
			t.Fatalf("Decrement = (%d, %v), want (-5, nil)", got, err)
		}
	})

	t.Run("increment preserves the key's existing ttl", func(t *testing.T) {
		t.Parallel()
		s, advance := factory()
		if err := s.Put(ctx, "counter", []byte("1"), time.Second); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := s.Increment(ctx, "counter", 1); err != nil {
			t.Fatalf("Increment: %v", err)
		}
		advance(2 * time.Second)
		_, ok, err := s.Get(ctx, "counter")
		if err != nil || ok {
			t.Fatalf("Get(counter) after expiry = (_, %v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("flush clears owned keys", func(t *testing.T) {
		t.Parallel()
		s, _ := factory()
		if err := s.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := s.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		_, ok, err := s.Get(ctx, "k")
		if err != nil || ok {
			t.Fatalf("Get(k) after Flush = (_, %v, %v), want (false, nil)", ok, err)
		}
	})
}

// TestFlushRequiresPrefix asserts that Flush refuses to run on a Store with
// no key prefix, returning ErrNoPrefix. Call this with a Store built with
// an empty prefix; it's separate from TestConformance because that suite's
// Store must carry a real prefix to exercise a successful Flush.
func TestFlushRequiresPrefix(t *testing.T, s Store) {
	t.Helper()
	err := s.Flush(context.Background())
	if !errors.Is(err, ErrNoPrefix) {
		t.Fatalf("Flush() = %v, want %v", err, ErrNoPrefix)
	}
}
