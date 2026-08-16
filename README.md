# chandlery

Small, independent Go packages I reuse across projects — no shared runtime,
no framework lock-in. Import only what you need.

```
go get github.com/nanaaikinson/chandlery
```

Requires Go 1.26.3+.

## Packages

- [`env`](env) — typed environment variable access (`GetInt`, `GetBool`,
  `GetSlice`, ...) plus `.env` file loading, built on the standard library.
- [`validator`](validator) — turns [zog](https://github.com/Oudwins/zog)
  validation issues into a per-field error shape suitable for an API
  response.
- [`db`](db) — a generic `bun`-backed repository (`db.Repository[T]`) and
  ULID-keyed base models, dialect-agnostic. Driver-specific pieces (a
  connection builder, Postgres error classification) live in their own
  subpackage: [`db/postgres`](db/postgres), with room for a `db/mysql` or
  similar alongside it later.
- [`respond`](respond) — a standard JSON response envelope, framework-agnostic
  at its core, with adapters for [`net/http`](respond/nethttp) and
  [Fiber v3](respond/fiber).
- [`cache`](cache) — a driver-agnostic `Store` contract (`Get`, `Put`, `Add`,
  `Forever`, `Pull`, `Forget`, `Has`, `Increment`/`Decrement`, `Flush`), with
  backends in their own subpackage: [`cache/memory`](cache/memory) (in-process
  map) and [`cache/redis`](cache/redis) (real Redis, via
  [go-redis](https://github.com/redis/go-redis)).

Each package is usable on its own; none of them import each other except
where noted (the `respond` adapters depend on `respond`'s core, and the
`cache` backends depend on `cache`'s core).

## Status

Early — API may still shift before v1. `storage` (local/S3) is planned but
not yet implemented, so it isn't part of this release.

## License

MIT — see [LICENSE](LICENSE).
