# cache

`Store` — the driver-agnostic cache contract: `Get`, `Put`, `Add`
(write-if-absent), `Forever` (no expiry), `Pull` (get-and-remove), `Forget`,
`Has`, `Increment`/`Decrement`, `Flush`. All keys are relative — a driver
applies its own prefix, so callers never spell one out.

Every backend also gets held to the same suite: `TestConformance(t, factory)`
exercises the full `Store` contract against whatever `factory` builds, and
`TestFlushRequiresPrefix(t, store)` separately checks that `Flush` refuses to
run without a prefix (`ErrNoPrefix`) — a store built with an empty prefix has
no way to tell its own keys apart from ones a wildcard delete shouldn't touch.

Backend-specific pieces live in their own subpackage:

- [`cache/memory`](memory) — an in-process, mutex-guarded map. No
  persistence, no sharing across processes; `New(prefix, opts...)` takes a
  `WithClock` option so tests can fast-forward TTL expiry instead of
  sleeping.
- [`cache/redis`](redis) — backed by a real Redis server
  ([go-redis](https://github.com/redis/go-redis)). `New(prefix)` connects
  using `REDIS_URL` (via `Addr()`); `Increment`/`Decrement` ride Redis's own
  atomic `INCRBY`/`DECRBY` (erroring on overflow, same as `cache/memory`'s
  own overflow check — the two backends give identical answers at the
  int64 boundary, not silently different ones), and `Flush` walks `SCAN` +
  `DEL` in batches rather than `KEYS`, which would block the whole server.
  `prefix` is glob-escaped before being used as `SCAN`'s `MATCH` pattern, so
  a prefix containing a literal `*`/`?`/`[`/`]` still means exactly that
  string, not a wildcard. Unlike `cache/memory`'s `Flush` (a single atomic
  map swap), this is several round trips — a `Put` racing a `Flush` on the
  same store can observe its key surviving. Its `Store` also
  exposes `Client() *redis.Client` for callers that need Redis features the
  `cache.Store` contract doesn't cover (pub/sub, pipelines, Lua scripts,
  ...) — that method isn't part of the `Store` interface, so reaching it
  needs the concrete `*redis.Store`, not a `cache.Store` value (see below).

A future backend (memcached or similar) would get the same treatment: its
own `cache/memcached` subpackage, plus a `TestConformance` run in its test
suite, `cache`'s `Store` contract reused as-is.

## Reaching a backend's own client

`Client()` isn't on `cache.Store` — a `cache.Store`-typed value can't call
it. It's reachable in either of two ways:

```go
// 1. Keep the concrete type instead of upcasting to cache.Store.
store, err := redis.New("myapp:")
store.Client().Ping(ctx) // *redis.Client, no assertion needed

// 2. Or type-assert a cache.Store value back down when you know the backend.
var s cache.Store = store
if rs, ok := s.(interface{ Client() *goredis.Client }); ok {
    rs.Client().Ping(ctx)
}
```

Reach for this only when `cache.Store` genuinely can't do what you need —
using it routinely defeats the point of coding against the interface.
