//go:build integration

package redis_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/cache/redis"
)

// prefixCounter gives every factory call its own namespace, so
// cache.TestConformance's subtests can run in parallel against the one
// shared Redis container without their keys colliding.
var prefixCounter atomic.Int64

func TestConformance(t *testing.T) {
	t.Parallel()

	cache.TestConformance(t, func() (cache.Store, func(time.Duration)) {
		prefix := fmt.Sprintf("conformance:%d:", prefixCounter.Add(1))
		s, err := redis.New(prefix)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s, realAdvance
	})
}

// realAdvance stands in for cache.TestConformance's fake-clock hook against
// a real Redis server, whose TTL clock nothing here can fast-forward.
// cache.TestConformance only ever calls advance with two kinds of value:
// a couple of real seconds, to prove a short ttl actually expired (sleep
// for real), or 24h/365d, to prove a non-expiring key survives an
// arbitrarily long wait (any short real sleep already proves that, so cap
// it instead of blocking the test for a day). If conformance.go's chosen
// magnitudes ever change, this threshold needs to move with them.
func realAdvance(d time.Duration) {
	if d >= time.Hour {
		d = 300 * time.Millisecond
	}
	time.Sleep(d)
}

func TestFlushRequiresPrefix(t *testing.T) {
	t.Parallel()

	s, err := redis.New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cache.TestFlushRequiresPrefix(t, s)
}

func TestFlush_OnlyDeletesOwnPrefix(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	owned, err := redis.New(fmt.Sprintf("flush:%d:", prefixCounter.Add(1)))
	if err != nil {
		t.Fatalf("New (owned): %v", err)
	}
	other, err := redis.New(fmt.Sprintf("flush:%d:", prefixCounter.Add(1)))
	if err != nil {
		t.Fatalf("New (other): %v", err)
	}

	if err := owned.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Put (owned): %v", err)
	}
	if err := other.Put(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Put (other): %v", err)
	}

	if err := owned.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	_, ok, err := owned.Get(ctx, "k")
	if err != nil || ok {
		t.Fatalf("Get (owned) after Flush = (_, %v, %v), want (false, nil)", ok, err)
	}
	_, ok, err = other.Get(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Get (other) after Flush = (_, %v, %v), want (true, nil) — Flush reached keys it doesn't own", ok, err)
	}
}
