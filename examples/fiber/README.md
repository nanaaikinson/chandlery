# fiber example

A small task API wiring every chandlery package together behind
[Fiber v3](https://github.com/gofiber/fiber): config via [`env`](../../env),
storage via [`db`](../../db)/[`db/postgres`](../../db/postgres), a
read-through cache via [`cache`](../../cache) (`cache/memory` or
`cache/redis`), file attachments via [`storage`](../../storage)
(`storage/local` or `storage/s3`), request validation via
[`validator/fiber`](../../validator/fiber), and the response envelope /
centralized error mapping via [`respond/fiber`](../../respond/fiber).

See [`../nethttp`](../nethttp) for the same API built on the standard
library instead, and [`../internal`](../internal) for the domain code
(`Task`, its request/validation schemas, the cache and storage driver
switches) both examples share unchanged.

## Run it

Requires a reachable Postgres (Redis only if you set `CACHE_DRIVER=redis`;
MinIO or another S3-compatible endpoint only if you set `STORAGE_DRIVER=s3`
— the default, `local`, writes attachments straight to disk, no extra
service needed):

```
docker run -d --name chandlery-example-pg -p 5432:5432 \
  -e POSTGRES_PASSWORD=postgres postgres:16-alpine

cp .env.example .env   # adjust DB_URL/PORT/CACHE_DRIVER/STORAGE_DRIVER if needed
go run .
```

The `tasks` table is created on startup (`create table if not exists`) — no
migration tool needed to try this out. A real service would use one instead;
see [Simplifications](#simplifications).

## Endpoints

| Method | Path                     | Body                               | Notes                                            |
| ------ | ------------------------ | ----------------------------------- | ------------------------------------------------- |
| GET    | `/healthz`               | —                                    | 503 if Postgres is unreachable                     |
| POST   | `/tasks`                 | `{"title": "...", "done": false}`   | full create; 422 with per-field errors on bad input |
| GET    | `/tasks`                 | —                                    | `?limit=` (1-100, default 20), `?offset=`          |
| GET    | `/tasks/:id`             | —                                    | cache-aside: served from cache when warm           |
| PATCH  | `/tasks/:id`             | `{"title": "..."}` and/or `{"done": true}` | true partial update; 400 if both fields omitted, 404 if the id doesn't exist |
| DELETE | `/tasks/:id`             | —                                    | idempotent: 204 whether or not the id existed      |
| PUT    | `/tasks/:id/attachment`  | raw file bytes                       | 404 if the task doesn't exist                      |
| GET    | `/tasks/:id/attachment`  | —                                    | streams the file back; 404 if there's none         |
| DELETE | `/tasks/:id/attachment`  | —                                    | idempotent: 204 either way                         |
| GET    | `/tasks/:id/attachment/url` | —                                 | a time-limited signed download link, not the file itself |

```
curl -X POST localhost:8080/tasks -d '{"title": "write the README", "done": false}'
curl localhost:8080/tasks
curl localhost:8080/tasks/<id>
curl -X PATCH localhost:8080/tasks/<id> -d '{"done": true}'
curl -X PUT localhost:8080/tasks/<id>/attachment --data-binary @photo.jpg
curl localhost:8080/tasks/<id>/attachment/url
curl -X DELETE localhost:8080/tasks/<id>
```

A malformed body renders as 400; a body that parses but fails validation
(missing/blank `title` on create, over 200 chars, or a `PATCH` with neither
field set) renders as 422 (create) or 400 (empty patch) with a per-field
`errors` array where applicable — both via `validator/fiber.ValidateRequest`.
`PATCH` only touches the fields it's given: `task.PatchRequest`'s `Title`/
`Done` are pointers, so an omitted field is distinguishable from an explicit
zero value and is left untouched in the row (a `{"title": "..."}` patch
can't accidentally reset `done` back to `false`).

## How the pieces fit

- **`main.go`** — loads config, opens the Postgres connection, ensures the
  schema, picks a cache backend and a storage backend, wires routes, and
  runs the server with a graceful shutdown on SIGINT/SIGTERM. Every route is
  wrapped in Fiber's `middleware/timeout`: fasthttp (which Fiber is built
  on) has no per-request context of its own, so `Ctx.Context()` is
  otherwise a `context.Background()` that never cancels or times out,
  however long a handler runs — wrapping each route is what gives handlers
  a real, deadline-bound, cancelable context to pass to `postgres.Ping`/
  `db.Repository`/`cache.Store`/`storage.Disk` calls. When
  `STORAGE_DRIVER=local`, it also registers `GET /storage/*`
  (`serveSignedLocalFile`): `storage/local`'s signed URLs aren't served by
  anything on their own (see that package's own README) — this route is
  the "whatever handler does" its doc comments point to, verifying the
  signature via `VerifySignedURL` before serving the file. `s3`'s signed
  URLs point straight at the S3 endpoint, so nothing here needs to serve
  them.
- **`handlers.go`** — one method per route, using `Task`/`CreateRequest`/
  `PatchRequest`/`List` from [`examples/internal/task`](../internal/task)
  and `ClampInt` from [`examples/internal/app`](../internal/app). `getTask`
  is cache-aside (check cache, fall through to Postgres, populate cache in
  the background on a miss — the fill is fire-and-forget, since its outcome
  only matters to a later request); `updateTask` uses `db.ConditionalUpdate`
  with a conditionally-built `SET` clause so a 404 on a missing id is one
  round trip, not a check-then-update race, and only the provided fields are
  set; `deleteTask` is unconditionally idempotent (no pre-check, always
  204); both `updateTask` and `deleteTask` invalidate the cache afterward,
  logging rather than failing the request if that invalidation errors — the
  write it follows already committed, so a 500 there would misreport a
  successful mutation as failed. `attachmentURL` hands back a signed link
  rather than streaming the file itself, so a real client downloads
  straight from S3 (or, on `local`, from this app's own `/storage` route)
  instead of doubling this process's bandwidth as a proxy — `downloadAttachment`
  still exists as the direct, always-available path either way.

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
- **An attachment's reported Content-Type differs by backend.**
  `task.AttachmentKey` has no file extension (an attachment could be
  anything), so `storage/s3`'s upload-time, extension-guessed Content-Type
  always comes back generic (`application/octet-stream`); `storage/local`
  re-sniffs the real bytes on every read regardless. A real service that
  cares about this would store the client-declared content type as its own
  column instead of leaning on either backend's guess.
- **No auth, no rate limiting.** Neither is a chandlery package concern;
  they'd sit in front of these handlers (middleware, a gateway) in a real
  deployment.
