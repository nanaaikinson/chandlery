package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrFileNotFound is returned when an operation targets a path that doesn't exist.
var ErrFileNotFound = errors.New("storage: file not found")

// ErrNoPrefix is returned by DeleteDirectory on a store with no key prefix,
// where a wildcard delete would reach paths this package doesn't own —
// the storage.Disk analogue of cache.ErrNoPrefix. storage/local has no
// equivalent risk (each Disk is already sandboxed to its own root
// directory, so DeleteDirectory can never reach another Disk's files
// regardless of prefix) and never returns it; only a backend sharing one
// namespace across multiple Disks (storage/s3, sharing a bucket) needs
// this guard.
var ErrNoPrefix = errors.New("storage: refusing to delete a directory on an unprefixed store")

// Disk is a single filesystem driver, mirroring the operations Laravel
// exposes on Storage::disk('name').
type Disk interface {
	// Put writes contents to path, creating/overwriting it.
	Put(ctx context.Context, path string, contents io.Reader) error
	// PutString writes a string to path, creating/overwriting it.
	PutString(ctx context.Context, path string, contents string) error
	// Get returns the full contents of path.
	Get(ctx context.Context, path string) ([]byte, error)
	// Exists reports whether path exists.
	Exists(ctx context.Context, path string) (bool, error)
	// Missing reports whether path does not exist.
	Missing(ctx context.Context, path string) (bool, error)
	// Delete removes one or more paths.
	Delete(ctx context.Context, paths ...string) error
	// Copy duplicates a file from one path to another.
	Copy(ctx context.Context, from string, to string) error
	// Move relocates a file from one path to another.
	Move(ctx context.Context, from string, to string) error
	// Size returns the size in bytes of the file at path.
	Size(ctx context.Context, path string) (int64, error)
	// LastModified returns the last-modified time of the file at path.
	LastModified(ctx context.Context, path string) (time.Time, error)
	// Url returns a public URL for path.
	Url(path string) string
	// TemporaryUrl returns a time-limited signed URL for path.
	TemporaryUrl(ctx context.Context, path string, expiration time.Duration) (string, error)
	// PresignedPutUrl returns a time-limited URL a client can PUT the object
	// at path to directly, without the bytes ever passing through this
	// process. On storage/s3 this is a real SigV4-signed URL usable against
	// the S3 endpoint directly; storage/local has no native equivalent, so
	// it mints its own HMAC-signed URL instead — one this package only
	// builds and verifies (via Disk.VerifySignedURL on the concrete
	// *local.Disk), it doesn't serve. Wiring an actual endpoint that calls
	// VerifySignedURL is up to the caller — see examples/fiber/main.go and
	// examples/nethttp/main.go's serveSignedLocalFile for a worked example
	// of that pattern on the GET/download side. Same shape to callers
	// either way, just a different signing scheme underneath.
	PresignedPutUrl(ctx context.Context, path string, expiration time.Duration) (string, error)
	// ContentType reports the stored content type of the object at path,
	// re-derived from the backend itself (S3's stored object metadata; a
	// content-sniff of the first bytes on local) rather than trusting
	// whatever a client declared at upload-request time.
	ContentType(ctx context.Context, path string) (string, error)
	// MakeDirectory creates a directory (and any parents) at path. On
	// backends with no native directory concept (e.g. S3), this creates a
	// marker object instead — but whether Exists/Missing on that exact path
	// then reports the directory as present is backend-specific (true on
	// storage/local, a real directory; false on storage/s3, whose marker
	// lives at a different key than the bare path), so don't rely on it
	// either way. Files/Directories (a delimited listing) and
	// AllFiles/AllDirectories (derived from every object's own path) both
	// recognize the directory on every backend regardless.
	MakeDirectory(ctx context.Context, path string) error
	// DeleteDirectory recursively removes the directory at path. Backends
	// differ in what "atomically" means here, the same way cache.Store's
	// Flush does across cache/memory and cache/redis: storage/local's is a
	// single os.Root.RemoveAll; storage/s3's is a listing followed by a
	// bulk delete over several round trips, so a Put racing a
	// DeleteDirectory under the same prefix can observe its object
	// surviving it.
	DeleteDirectory(ctx context.Context, path string) error
	// Files lists the files directly inside dir (non-recursive).
	Files(ctx context.Context, dir string) ([]string, error)
	// AllFiles recursively lists every file under dir.
	AllFiles(ctx context.Context, dir string) ([]string, error)
	// Directories lists the sub-directories directly inside dir (non-recursive).
	Directories(ctx context.Context, dir string) ([]string, error)
	// AllDirectories recursively lists every sub-directory under dir.
	AllDirectories(ctx context.Context, dir string) ([]string, error)
}
