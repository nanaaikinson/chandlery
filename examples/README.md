# examples

Two runnable programs, same task API, same set of chandlery packages — one
per HTTP framework this repo has an adapter for:

- [`fiber`](fiber) — built on [Fiber v3](https://github.com/gofiber/fiber).
- [`nethttp`](nethttp) — built on the standard library's `net/http`.
- [`internal`](internal) — the domain code both of the above share
  unchanged: `internal/task` (the `Task` model, its create/patch request
  and validation schemas, the paginated list shape, the cache and storage
  driver switches, task attachments) and `internal/app` (logging setup, a
  query-param clamp helper). Neither subpackage is framework-specific, and
  Go's `internal/` visibility rule keeps both scoped to `examples/...` —
  they aren't part of this module's public API.

Comparing `fiber` and `nethttp` side by side is the point: with the shared
domain code factored out into `internal`, what's left in each — `main.go`,
`handlers.go` — is close to line-for-line identical, differing only in the
adapter layer (`respond/fiber` vs `respond/nethttp`, `validator/fiber` vs
`validator/nethttp`) and the framework's own routing/middleware/context
handling.

Both wire together every package in this module:

| Package                                            | Used for                                              |
| --------------------------------------------------- | ------------------------------------------------------ |
| [`env`](../env)                                      | reading `PORT`/`DB_URL`/`CACHE_DRIVER`/`STORAGE_DRIVER`/etc. |
| [`db`](../db) + [`db/postgres`](../db/postgres)      | the `tasks` table, via `db.Repository[*Task]`          |
| [`cache`](../cache) (`memory` or `redis`)            | a read-through cache in front of task reads            |
| [`storage`](../storage) (`local` or `s3`)            | task attachments — upload, download, signed URLs       |
| [`validator`](../validator) (+ framework adapter)    | validating the create/update request body              |
| [`respond`](../respond) (+ framework adapter)        | the JSON envelope and centralized error → status map   |

Each example is fully standalone — its own `README.md`, its own
`.env.example`. Start with whichever framework you're already using; the
other is there to see the same problem solved through the other adapter.
