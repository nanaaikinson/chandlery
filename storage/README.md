# storage

`Disk` — the driver-agnostic filesystem contract, mirroring the operations
Laravel exposes on `Storage::disk('name')`: `Put`/`PutString`/`Get`,
`Exists`/`Missing`, `Delete`, `Copy`/`Move`, `Size`/`LastModified`/
`ContentType`, `Url`/`TemporaryUrl`/`PresignedPutUrl`, and directory
operations (`MakeDirectory`/`DeleteDirectory`,
`Files`/`AllFiles`/`Directories`/`AllDirectories`).

Every backend gets held to the same suite:
`TestConformance(t, factory)` exercises the full `Disk` contract against
whatever `factory` builds, so a bug in one backend's directory listing or
not-found handling can't hide behind the other backend's different
implementation.

Backend-specific pieces live in their own subpackage:

- [`storage/local`](local) — the local filesystem, sandboxed to a root
  directory via `os.Root` (Go 1.24+): a caller-supplied path can never read
  or write outside that root, symlinks included — stronger than a
  hand-rolled `filepath.Clean`-and-check. `New(root, opts...)` creates the
  root if it doesn't exist; `TemporaryUrl`/`PresignedPutUrl` mint an
  HMAC-signed URL (there's no native signed-URL mechanism for a plain
  filesystem) that `VerifySignedURL` checks back — this package only builds
  and verifies that URL, it doesn't serve one; wiring an actual download/
  upload endpoint that calls `VerifySignedURL` is up to the caller.
- [`storage/s3`](s3) — any S3-compatible object store (real AWS S3, MinIO,
  ...) via the [MinIO Go SDK](https://github.com/minio/minio-go), which
  speaks the S3 API rather than being MinIO-specific. `New(bucket, opts...)`
  connects using `S3_ENDPOINT`/`S3_ACCESS_KEY_ID`/`S3_SECRET_ACCESS_KEY`
  (via `Endpoint()`/`AccessKeyID()`/`SecretAccessKey()`), defaulting to
  MinIO's own out-of-the-box dev credentials so a local container works
  with zero configuration; call `EnsureBucket` once at startup if the
  bucket might not exist yet. `TemporaryUrl`/`PresignedPutUrl` are real
  SigV4-signed URLs (no local crypto scheme needed, unlike `storage/local`).
  Directory operations are all derived from S3's flat key namespace — a
  "directory" is either a zero-byte marker object at `path+"/"`
  (`MakeDirectory`) or inferred from every object's own key
  (`Files`/`Directories` via a `SCAN`-style delimited listing,
  `AllFiles`/`AllDirectories` via a recursive one).

A future backend (GCS, Azure Blob, ...) would get the same treatment: its
own `storage/gcs` subpackage, `storage`'s `Disk` contract and conformance
suite reused as-is.

## `ErrFileNotFound`

Both backends translate their own not-found response (`fs.ErrNotExist` for
local, S3's `NoSuchKey` for s3) into `storage.ErrFileNotFound`, so callers
write one `errors.Is` check regardless of which `Disk` they're holding.

## Reaching a backend's own client

Neither backend currently exposes an underlying-client escape hatch the way
`cache/redis.Store.Client()` does — nothing in this package has needed one
yet. If that changes, it'll follow the same pattern: a method on the
concrete `*local.Disk`/`*s3.Disk` type, not part of the `Disk` interface,
reachable by keeping the concrete type or a type assertion off a `Disk`
value.
