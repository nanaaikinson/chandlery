package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/nanaaikinson/chandlery/storage"
	"github.com/nanaaikinson/chandlery/storage/local"
	"github.com/nanaaikinson/chandlery/storage/s3"
)

// NewStorageDisk builds the storage.Disk an example uses to store task
// attachments, chosen by driver ("local" or "s3"). It always returns a
// close func — a no-op for s3, which holds no resource that needs
// releasing, unlike local's os.Root handle — so callers can defer it
// unconditionally regardless of which backend was picked.
//
// Unlike NewCacheStore, this does real network I/O for the s3 driver
// (EnsureBucket), so it takes a ctx: a first run against a fresh MinIO has
// no bucket yet, and failing fast here beats failing on the first upload.
// localBaseURL is only used by the local driver, to build a signed
// TemporaryUrl/PresignedPutUrl that actually resolves back to this app's
// own /storage route (see main.go) — s3's signed URLs point at the S3
// endpoint directly and need no such base. s3Prefix scopes s3 keys the same
// way NewCacheStore's own prefix argument scopes cache keys — without it,
// two apps pointed at the same default S3_BUCKET would share one flat
// keyspace and could read or delete each other's attachments.
func NewStorageDisk(ctx context.Context, driver, localRoot, localBaseURL, bucket, s3Prefix string) (storage.Disk, func() error, error) {
	switch driver {
	case "", "local":
		d, err := local.New(localRoot, local.WithBaseURL(localBaseURL))
		if err != nil {
			return nil, nil, err
		}
		return d, d.Close, nil
	case "s3":
		d, err := s3.New(bucket, s3.WithPrefix(s3Prefix))
		if err != nil {
			return nil, nil, err
		}
		if err := d.EnsureBucket(ctx); err != nil {
			return nil, nil, err
		}
		return d, func() error { return nil }, nil
	default:
		return nil, nil, fmt.Errorf("storage: unknown STORAGE_DRIVER %q (want \"local\" or \"s3\")", driver)
	}
}

// MaxAttachmentSize caps a single attachment upload. Applied explicitly by
// both examples (Fiber's own Config.BodyLimit and net/http's
// http.MaxBytesReader) rather than left to each framework's own default:
// Fiber's is 4MiB out of the box, net/http's is unbounded, and those two
// defaults disagreeing would make the "same API" the two examples claim to
// be reject the identical oversized upload differently depending only on
// which one happened to be running.
const MaxAttachmentSize = 32 << 20 // 32MiB

// AttachmentURL is the response shape for a signed, time-limited download
// link.
type AttachmentURL struct {
	URL              string `json:"url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

// AttachmentKey builds the storage path a task's attachment is stored
// under. It deliberately has no file extension (a task's attachment could
// be anything) — storage/local's ContentType re-sniffs the real type from
// the bytes regardless, but storage/s3's Content-Type is guessed from the
// key's extension at upload time, so an S3-backed attachment always reports
// back as generic application/octet-stream. A real service that cares
// about that would store the client-declared content type as its own
// column instead of leaning on the backend's guess.
//
// id is stripped of "/" and ".." before use: it's a route param, normally a
// server-generated ULID, but a request can put any string there. storage/s3
// has no path validation of its own, and storage/local's os.Root only
// blocks a full escape from its root — neither stops an id like ".."
// resolving to a location outside this task's own tasks/<id>/ scope, so
// this closes that off at the one place every attachment path gets built.
func AttachmentKey(id string) string {
	id = strings.ReplaceAll(id, "/", "")
	id = strings.ReplaceAll(id, "..", "")
	return "tasks/" + id + "/attachment"
}
