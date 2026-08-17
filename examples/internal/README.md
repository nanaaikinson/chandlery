# internal

Code shared by [`../fiber`](../fiber) and [`../nethttp`](../nethttp) that
isn't specific to either — kept under `internal/` so Go's own visibility
rule scopes it to `examples/...`, not this module's public API.

- [`task`](task) — the domain: the `Task` model, `CreateRequest`/
  `CreateSchema` (POST, full create), `PatchRequest`/`PatchSchema` (PATCH,
  true partial update via pointer fields), `List` (the paginated response
  shape), `NewCacheStore` (the `CACHE_DRIVER` switch between `cache/memory`
  and `cache/redis`), `CacheKey`, `NewStorageDisk` (the `STORAGE_DRIVER`
  switch between `storage/local` and `storage/s3`), `AttachmentKey`, and
  `AttachmentURL` (the signed-download-link response shape).
- [`app`](app) — the rest: `ConfigureLogger` (text locally, JSON otherwise)
  and `ClampInt` (a query-param parse-and-clamp helper).

Both examples import these unchanged. What's left in each example's own
`main.go`/`handlers.go` is exactly the part that differs between the two
frameworks — that's deliberate: it's what makes comparing the two side by
side (see the top-level [`examples/README.md`](../README.md)) actually show
the adapter difference instead of incidental copy-paste noise.
