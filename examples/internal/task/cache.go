package task

import (
	"fmt"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/cache/memory"
	"github.com/nanaaikinson/chandlery/cache/redis"
)

// NewCacheStore builds the cache.Store an example uses to cache task reads
// ahead of Postgres, chosen by driver ("memory" or "redis"). It always
// returns a close func — a no-op for memory — so callers can defer it
// unconditionally regardless of which backend was picked.
func NewCacheStore(driver, prefix string) (cache.Store, func() error, error) {
	switch driver {
	case "", "memory":
		return memory.New(prefix), func() error { return nil }, nil
	case "redis":
		store, err := redis.New(prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("cache: %w", err)
		}
		return store, store.Close, nil
	default:
		return nil, nil, fmt.Errorf("cache: unknown CACHE_DRIVER %q (want \"memory\" or \"redis\")", driver)
	}
}

// CacheKey builds the cache key a task is stored under.
func CacheKey(id string) string {
	return "task:" + id
}
