// Package redis implements cache.Store against a real Redis server.
package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/nanaaikinson/chandlery/cache"
	"github.com/nanaaikinson/chandlery/env"
)

// Store is a cache.Store backed by a Redis client. Keys are namespaced
// under prefix in the shared Redis keyspace, so Flush's SCAN only ever
// touches keys this Store owns.
type Store struct {
	client *goredis.Client
	prefix string
}

var _ cache.Store = (*Store)(nil)

// Addr returns the Redis connection URL from REDIS_URL, falling back to a
// local default both when unset and when present-but-blank (as it might
// ship in a checked-in .env.example), since a blank URL can never be a
// deliberate value.
func Addr() string {
	addr := env.Get("REDIS_URL", "")
	if addr == "" {
		addr = "redis://127.0.0.1:6379/0"
	}
	return addr
}

// New opens a Store against the Redis server at Addr(). prefix gates
// Flush: an empty prefix makes Flush return cache.ErrNoPrefix.
func New(prefix string) (*Store, error) {
	addr := Addr()
	opts, err := goredis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("cache/redis: parsing %s: %w", addr, err)
	}
	return &Store{client: goredis.NewClient(opts), prefix: prefix}, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	return s.client.Close()
}

// Client returns the *redis.Client this Store runs commands through, for
// callers that need Redis features cache.Store doesn't expose (pub/sub,
// pipelines, Lua scripts, ...).
func (s *Store) Client() *goredis.Client {
	return s.client
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.client.Get(ctx, s.key(key)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *Store) Put(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return s.client.Set(ctx, s.key(key), val, expiryFor(ttl)).Err()
}

func (s *Store) Add(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, s.key(key), val, expiryFor(ttl)).Result()
}

func (s *Store) Forever(ctx context.Context, key string, val []byte) error {
	return s.client.Set(ctx, s.key(key), val, 0).Err()
}

func (s *Store) Pull(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := s.client.GetDel(ctx, s.key(key)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (s *Store) Forget(ctx context.Context, key string) error {
	return s.client.Del(ctx, s.key(key)).Err()
}

func (s *Store) Has(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, s.key(key)).Result()
	return n > 0, err
}

// Increment and Decrement rely on Redis's own INCRBY/DECRBY, which are
// atomic and (unlike a Set) leave an existing key's ttl untouched.

func (s *Store) Increment(ctx context.Context, key string, by int64) (int64, error) {
	return s.client.IncrBy(ctx, s.key(key), by).Result()
}

func (s *Store) Decrement(ctx context.Context, key string, by int64) (int64, error) {
	return s.client.DecrBy(ctx, s.key(key), by).Result()
}

// flushBatchSize caps how many keys Flush deletes per DEL call. SCAN's own
// COUNT is only a per-iteration hint to Redis, not a hard limit on how many
// keys a single Next can return, so this also bounds each DEL's size.
const flushBatchSize = 1000

// Flush deletes every key under prefix via SCAN+DEL, in batches, rather
// than KEYS: KEYS blocks the whole server while it walks the entire
// keyspace, which in a shared Redis is a lot more than this Store owns.
// The MATCH pattern glob-escapes prefix first — SCAN's MATCH is a glob, not
// a literal-prefix match, so an unescaped prefix containing *, ?, [, ] or \
// would otherwise make Flush miss its own keys or reach ones it doesn't own.
func (s *Store) Flush(ctx context.Context) error {
	if s.prefix == "" {
		return cache.ErrNoPrefix
	}

	iter := s.client.Scan(ctx, 0, globEscape(s.prefix)+"*", flushBatchSize).Iterator()

	batch := make([]string, 0, flushBatchSize)
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= flushBatchSize {
			if err := s.client.Del(ctx, batch...).Err(); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		if err := s.client.Del(ctx, batch...).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) key(key string) string {
	return s.prefix + key
}

// globEscape backslash-escapes Redis glob metacharacters (*, ?, [, ], \) in
// s, so it can be used as a literal prefix inside a SCAN/KEYS MATCH pattern
// instead of being interpreted as a wildcard or character class.
func globEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// expiryFor maps the cache.Store contract's "ttl <= 0 means no expiry"
// onto go-redis's own "0 means no expiry" convention.
func expiryFor(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 0
	}
	return ttl
}
