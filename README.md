# chandlery

Small, independent Go packages I reuse across projects — no shared runtime,
no framework lock-in. Import only what you need.

```
go get github.com/nanaaikinson/chandlery
```

Requires Go 1.27.0+.

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
- [`odm`](odm) — a MongoDB object-document mapper over
  [mongo-driver/v2](https://go.mongodb.org/mongo-driver/v2): a generic,
  immutable query builder (`odm.Use[User](database).Where("age", ">=", 18).Get(ctx)`)
  with atomic updates, soft deletes, scopes, hooks and observers, cursor
  pagination, batched eager loading, transactions and index declarations —
  and the driver's own types never more than a `Raw()` away. Needs MongoDB
  8.0+. Independent of `db`: the two share no types and neither imports the
  other. Start with its [guide](odm/GUIDE.md).
- [`storage`](storage) — a driver-agnostic `Disk` contract mirroring
  Laravel's `Storage::disk()` (`Put`/`Get`, `Copy`/`Move`, `Url`/
  `TemporaryUrl`/`PresignedPutUrl`, directory listing, ...), with backends
  in their own subpackage: [`storage/local`](storage/local) (the local
  filesystem, sandboxed via `os.Root`) and [`storage/s3`](storage/s3) (any
  S3-compatible store, via the [MinIO Go SDK](https://github.com/minio/minio-go)).

Each package is usable on its own; none of them import each other except
where noted (the `respond` adapters depend on `respond`'s core, and the
`cache`/`storage` backends depend on their own package's core).

## Status

Early — API may still shift before v1.

## Releasing

Releases are automatic. Every push to `main` runs the full suite — including
the Docker-backed integration tests — then reads the commit messages since
the last tag ([Conventional Commits](https://www.conventionalcommits.org)),
tags the next version and publishes a GitHub release with generated notes:

| Commits since the last tag | Next version |
| --- | --- |
| `fix:` / `perf:` | patch — `v0.4.0` → `v0.4.1` |
| `feat:` | minor — `v0.4.0` → `v0.5.0` |
| `feat!:` / `BREAKING CHANGE:` footer | minor while on v0 (`v0.5.0`); major from v1 on |
| only `docs:`, `chore:`, `ci:`, `test:`, … | no release |

With squash merges, the PR title is the commit message — so it is what
decides the version.

A breaking change on v0 never promotes itself to v1. To choose the bump
yourself — v1.0.0 included — run the **Release** workflow from the Actions tab
and pick `patch`, `minor` or `major`. Pushing a tag by hand still works too,
and later automatic releases count on from it:

```bash
git tag -a v0.5.0-rc.1 -m "v0.5.0-rc.1" && git push origin v0.5.0-rc.1
```

A tag with a hyphen (`v0.5.0-rc.1`) is published as a prerelease, and doesn't
count as the base for the next automatic version.

The workflow refuses a tag Go can't use: it must be `vMAJOR.MINOR.PATCH`
(`0.4.0` without the `v` resolves for nobody), and from v2 on the major
version has to be in the module path too — tagging `v2.0.0` without renaming
the module to `.../v2` publishes a version nothing can import. So after v1, a
breaking change stops the release until the module is renamed. A published
version is immutable, so both are cheaper to catch before the tag than after.

## License

MIT — see [LICENSE](LICENSE).
