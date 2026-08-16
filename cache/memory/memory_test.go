package memory_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/cache/memory"
)

// fakeClock lets tests advance a Store's notion of "now" instead of
// sleeping for real TTLs to elapse.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestConformance(t *testing.T) {
	t.Parallel()

	cache.TestConformance(t, func() (cache.Store, func(time.Duration)) {
		clk := newFakeClock()
		return memory.New("test:", memory.WithClock(clk.Now)), clk.Advance
	})
}

func TestFlushRequiresPrefix(t *testing.T) {
	t.Parallel()

	cache.TestFlushRequiresPrefix(t, memory.New(""))
}
