# nethttp example

A small task API wiring every chandlery package together behind the
standard library's `net/http` (Go 1.22+ `ServeMux` method/path patterns, no
router dependency): config via [`env`](../../env), storage via
[`db`](../../db)/[`db/postgres`](../../db/postgres), a read-through cache
via [`cache`](../../cache) (`cache/memory` or `cache/redis`), request
validation via [`validator/nethttp`](../../validator/nethttp), and the
response envelope / centralized error mapping via
[`respond/nethttp`](../../respond/nethttp).

See [`../fiber`](../fiber) for the same API built on Fiber v3 instead, and
[`../internal`](../internal) for the domain code (`Task`, its
request/validation schemas, the cache driver switch) both examples share
unchanged.

## Run it

Requires a reachable Postgres (Redis only if you set `CACHE_DRIVER=redis`):

```
docker run -d --name chandlery-example-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres postgres:16-alpine

cp .env.example .env   # adjust DB_URL/PORT/CACHE_DRIVER if needed
go run .
```

The `tasks` table is created on startup (`create table if not exists`) — no
migration tool needed to try this out. A real service would use one instead;
see [Simplifications](#simplifications).

## Endpoints

| Method | Path          | Body                               | Notes                                            |
| ------ | ------------- | ----------------------------------- | ------------------------------------------------- |
| GET    | `/healthz`    | —                                    | 503 if Postgres is unreachable                     |
| POST   | `/tasks`      | `{"title": "...", "done": false}`   | full create; 422 with per-field errors on bad input |
| GET    | `/tasks`      | —                                    | `?limit=` (1-100, default 20), `?offset=`          |
| GET    | `/tasks/{id}` | —                                    | cache-aside: served from cache when warm           |
| PATCH  | `/tasks/{id}` | `{"title": "..."}` and/or `{"done": true}` | true partial update; 400 if both fields omitted, 404 if the id doesn't exist |
| DELETE | `/tasks/{id}` | —                                    | idempotent: 204 whether or not the id existed      |

```
curl -X POST localhost:8080/tasks -d '{"title": "write the README", "done": false}'
curl localhost:8080/tasks
curl localhost:8080/tasks/<id>
curl -X PATCH localhost:8080/tasks/<id> -d '{"done": true}'
curl -X DELETE localhost:8080/tasks/<id>
```

A malformed body renders as 400; a body that parses but fails validation
(missing/blank `title` on create, over 200 chars, or a `PATCH` with neither
field set) renders as 422 (create) or 400 (empty patch) with a per-field
`errors` array where applicable — both via
`validator/nethttp.ValidateRequest`. `PATCH` only touches the fields it's
given: `task.PatchRequest`'s `Title`/`Done` are pointers, so an omitted
field is distinguishable from an explicit zero value and is left untouched
in the row (a `{"title": "..."}` patch can't accidentally reset `done` back
to `false`).

## How the pieces fit

- **`main.go`** — loads config, opens the Postgres connection, ensures the
  schema, picks a cache backend, wires routes onto a plain `http.ServeMux`,
  and runs the server with a graceful shutdown on SIGINT/SIGTERM. The whole
  mux is wrapped in `http.TimeoutHandler` for a request deadline — net/http
  already gives every handler a real, cancelable `r.Context()` on its own
  (unlike Fiber; see `../fiber/main.go`), so this only adds the timeout on
  top, not the cancellation itself. `withRequestID` is this example's own
  small middleware: unlike Fiber, `net/http` has no built-in request-ID
  mechanism, so it generates one (a ULID, reusing `oklog/ulid` already in
  the module) and stores it via `respond/nethttp.WithRequestID` for the 5xx
  logging path to pick up.
- **`handlers.go`** — one method per route, each a `respondhttp.Handler`
  (`func(w, r) error`) composed with `respondhttp.Wrap` at registration,
  using `Task`/`CreateRequest`/`PatchRequest`/`List` from
  [`examples/internal/task`](../internal/task) and `ClampInt` from
  [`examples/internal/app`](../internal/app). `getTask` is cache-aside
  (check cache, fall through to Postgres, populate cache in the background
  on a miss — the fill is fire-and-forget, since its outcome only matters to
  a later request); `updateTask` uses `db.ConditionalUpdate` with a
  conditionally-built `SET` clause so a 404 on a missing id is one round
  trip, not a check-then-update race, and only the provided fields are set;
  `deleteTask` is unconditionally idempotent (no pre-check, always 204);
  both `updateTask` and `deleteTask` invalidate the cache afterward, logging
  rather than failing the request if that invalidation errors — the write
  it follows already committed, so a 500 there would misreport a successful
  mutation as failed.

## Simplifications

Called out explicitly rather than left implicit, since this is meant to be
read, not just run:

- **Schema via `create table if not exists`, not a migration tool.** Fine
  for one table in an example; a real service wants versioned migrations.
- **Cache invalidation has a known race.** Between `ConditionalUpdate`/
  `Delete` committing and the following `cache.Forget`, a concurrent `GET`
  could still repopulate the cache with the now-stale value. Closing that
  gap needs a distributed lock or a different invalidation strategy
  (write-through, versioned keys) — out of scope for what this example is
  demonstrating.
- **`CACHE_TTL_SECONDS=0` disables caching for `getTask`, rather than
  reaching `cache.Store`'s own "`ttl <= 0` means no expiry."** An operator
  setting it to `0` expecting "don't cache this" would otherwise get entries
  that are cached forever instead — see `handlers.go`'s `cachingEnabled`.
- **No auth, no rate limiting.** Neither is a chandlery package concern;
  they'd sit in front of this mux (middleware, a gateway) in a real
  deployment.
