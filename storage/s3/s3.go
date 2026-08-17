// Package s3 implements storage.Disk against any S3-compatible object store,
// via the MinIO Go SDK (which speaks the S3 API — this works against real
// AWS S3 as well as MinIO, not just MinIO itself).
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/nanaaikinson/chandlery/env"
	"github.com/nanaaikinson/chandlery/storage"
)

// Disk is a storage.Disk backed by an S3-compatible bucket. Keys are
// namespaced under prefix, the same way cache/redis namespaces its keys —
// several Disks can safely share one bucket.
type Disk struct {
	client        *minio.Client
	bucket        string
	prefix        string
	publicBaseURL string
}

var _ storage.Disk = (*Disk)(nil)

// Endpoint returns the S3 endpoint (host:port, no scheme) from S3_ENDPOINT,
// falling back to a local default both when unset and when
// present-but-blank, since a blank endpoint can never be a deliberate value.
func Endpoint() string {
	endpoint := env.Get("S3_ENDPOINT", "")
	if endpoint == "" {
		endpoint = "127.0.0.1:9000"
	}
	return endpoint
}

// AccessKeyID returns S3_ACCESS_KEY_ID, falling back to MinIO's own default
// dev credential both when unset and when present-but-blank (same rationale
// as Endpoint), so a local MinIO container works with zero configuration.
func AccessKeyID() string {
	id := env.Get("S3_ACCESS_KEY_ID", "")
	if id == "" {
		id = "minioadmin"
	}
	return id
}

// SecretAccessKey returns S3_SECRET_ACCESS_KEY, falling back to match
// AccessKeyID's default under the same unset-or-blank rule.
func SecretAccessKey() string {
	secret := env.Get("S3_SECRET_ACCESS_KEY", "")
	if secret == "" {
		secret = "minioadmin"
	}
	return secret
}

// Region returns S3_REGION, defaulting to "" (letting the SDK/server pick).
func Region() string { return env.Get("S3_REGION", "") }

// Option configures a Disk.
type Option func(*Disk)

// WithPrefix scopes every key this Disk touches under prefix, so several
// Disks (or applications) can safely share one bucket. A trailing "/" is
// added automatically if missing.
func WithPrefix(prefix string) Option {
	return func(d *Disk) {
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		d.prefix = prefix
	}
}

// WithPublicBaseURL overrides Url's base (e.g. a CDN domain fronting the
// bucket) instead of deriving it from the S3 endpoint itself.
func WithPublicBaseURL(base string) Option {
	return func(d *Disk) { d.publicBaseURL = strings.TrimSuffix(base, "/") }
}

// New opens a Disk against bucket on the server at Endpoint(). It doesn't
// perform any network I/O itself (the underlying client dials lazily, same
// as db/postgres.New and cache/redis.New) — call EnsureBucket if the bucket
// might not exist yet.
func New(bucket string, opts ...Option) (*Disk, error) {
	client, err := minio.New(Endpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(AccessKeyID(), SecretAccessKey(), ""),
		Secure: env.GetBool("S3_USE_SSL", false),
		Region: Region(),
	})
	if err != nil {
		return nil, fmt.Errorf("storage/s3: %w", err)
	}

	d := &Disk{client: client, bucket: bucket}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// EnsureBucket creates the Disk's bucket if it doesn't already exist. Call
// this once at startup (like db/postgres.Ping, or applying a schema) rather
// than on every operation.
func (d *Disk) EnsureBucket(ctx context.Context) error {
	exists, err := d.client.BucketExists(ctx, d.bucket)
	if err != nil {
		return fmt.Errorf("storage/s3: checking bucket %s: %w", d.bucket, err)
	}
	if exists {
		return nil
	}
	if err := d.client.MakeBucket(ctx, d.bucket, minio.MakeBucketOptions{Region: Region()}); err != nil {
		return fmt.Errorf("storage/s3: creating bucket %s: %w", d.bucket, err)
	}
	return nil
}

func (d *Disk) Put(ctx context.Context, p string, contents io.Reader) error {
	_, err := d.client.PutObject(ctx, d.bucket, d.key(p), contents, -1, minio.PutObjectOptions{
		ContentType: contentTypeFor(p),
	})
	if err != nil {
		return fmt.Errorf("storage/s3: putting %s: %w", p, err)
	}
	return nil
}

func (d *Disk) PutString(ctx context.Context, p string, contents string) error {
	return d.Put(ctx, p, strings.NewReader(contents))
}

func (d *Disk) Get(ctx context.Context, p string) ([]byte, error) {
	obj, err := d.client.GetObject(ctx, d.bucket, d.key(p), minio.GetObjectOptions{})
	if err != nil {
		return nil, translateErr(p, err)
	}
	// GetObject is lazy: the request isn't actually made (and a missing key
	// doesn't surface) until the first read.
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, translateErr(p, err)
	}
	return data, nil
}

func (d *Disk) Exists(ctx context.Context, p string) (bool, error) {
	_, err := d.client.StatObject(ctx, d.bucket, d.key(p), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage/s3: checking %s: %w", p, err)
	}
	return true, nil
}

func (d *Disk) Missing(ctx context.Context, p string) (bool, error) {
	exists, err := d.Exists(ctx, p)
	return !exists, err
}

func (d *Disk) Delete(ctx context.Context, paths ...string) error {
	var errs []error
	for _, p := range paths {
		// S3's DELETE is already idempotent (no error for a missing key),
		// so there's no fs.ErrNotExist-equivalent to swallow here.
		if err := d.client.RemoveObject(ctx, d.bucket, d.key(p), minio.RemoveObjectOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("storage/s3: %w", errors.Join(errs...))
	}
	return nil
}

func (d *Disk) Copy(ctx context.Context, from, to string) error {
	_, err := d.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: d.bucket, Object: d.key(to)},
		minio.CopySrcOptions{Bucket: d.bucket, Object: d.key(from)},
	)
	if err != nil {
		return translateErr(from, err)
	}
	return nil
}

// Move copies then deletes, unlike storage/local's Move (a single atomic
// os.Root.Rename): a failure on the delete step after a successful copy
// leaves from and to both holding the content, rather than the all-or-
// nothing outcome Rename guarantees.
func (d *Disk) Move(ctx context.Context, from, to string) error {
	if err := d.Copy(ctx, from, to); err != nil {
		return err
	}
	return d.Delete(ctx, from)
}

func (d *Disk) Size(ctx context.Context, p string) (int64, error) {
	info, err := d.client.StatObject(ctx, d.bucket, d.key(p), minio.StatObjectOptions{})
	if err != nil {
		return 0, translateErr(p, err)
	}
	return info.Size, nil
}

func (d *Disk) LastModified(ctx context.Context, p string) (time.Time, error) {
	info, err := d.client.StatObject(ctx, d.bucket, d.key(p), minio.StatObjectOptions{})
	if err != nil {
		return time.Time{}, translateErr(p, err)
	}
	return info.LastModified, nil
}

func (d *Disk) ContentType(ctx context.Context, p string) (string, error) {
	info, err := d.client.StatObject(ctx, d.bucket, d.key(p), minio.StatObjectOptions{})
	if err != nil {
		return "", translateErr(p, err)
	}
	return info.ContentType, nil
}

// Url returns d.publicBaseURL (if set) or the S3 endpoint itself as the
// base, joined with the bucket and key. Neither form is signed — this is
// only meaningful for a public(-ish) bucket; use TemporaryUrl otherwise.
func (d *Disk) Url(p string) string {
	base := d.publicBaseURL
	if base == "" {
		base = strings.TrimSuffix(d.client.EndpointURL().String(), "/")
	}
	return base + "/" + d.bucket + "/" + d.key(p)
}

func (d *Disk) TemporaryUrl(ctx context.Context, p string, expiration time.Duration) (string, error) {
	u, err := d.client.PresignedGetObject(ctx, d.bucket, d.key(p), expiration, url.Values{})
	if err != nil {
		return "", fmt.Errorf("storage/s3: signing %s: %w", p, err)
	}
	return u.String(), nil
}

func (d *Disk) PresignedPutUrl(ctx context.Context, p string, expiration time.Duration) (string, error) {
	u, err := d.client.PresignedPutObject(ctx, d.bucket, d.key(p), expiration)
	if err != nil {
		return "", fmt.Errorf("storage/s3: signing %s: %w", p, err)
	}
	return u.String(), nil
}

// MakeDirectory creates a zero-byte marker object at p+"/", S3's usual
// stand-in for a directory: Files/Directories (non-recursive, via a "/"
// delimiter) and AllFiles/AllDirectories (derived from every object's own
// path) both recognize it, but Exists(p) does not — the interface's Exists
// is about objects, and a directory marker is one only incidentally.
func (d *Disk) MakeDirectory(ctx context.Context, p string) error {
	dirKey := strings.TrimSuffix(d.key(p), "/") + "/"
	_, err := d.client.PutObject(ctx, d.bucket, dirKey, bytes.NewReader(nil), 0, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage/s3: making directory %s: %w", p, err)
	}
	return nil
}

// DeleteDirectory removes every object (files and any directory markers)
// under p, via a recursive listing fed straight into a bulk delete. Refuses
// to run on an unprefixed Disk (storage.ErrNoPrefix), the same way
// cache/redis.Store.Flush refuses on an unprefixed cache: an unprefixed
// Disk shares its bucket's entire keyspace with anything else pointed at
// that bucket, and a recursive delete rooted there would reach far more
// than this Disk owns.
func (d *Disk) DeleteDirectory(ctx context.Context, p string) error {
	if d.prefix == "" {
		return storage.ErrNoPrefix
	}

	prefix := strings.TrimSuffix(d.key(p), "/") + "/"
	objects := d.client.ListObjectsIter(ctx, d.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})

	results, err := d.client.RemoveObjectsWithIter(ctx, d.bucket, objects, minio.RemoveObjectsOptions{})
	if err != nil {
		return fmt.Errorf("storage/s3: deleting directory %s: %w", p, err)
	}

	var errs []error
	for result := range results {
		if result.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", result.ObjectName, result.Err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("storage/s3: deleting directory %s: %w", p, errors.Join(errs...))
	}
	return nil
}

func (d *Disk) Files(ctx context.Context, dir string) ([]string, error) {
	items, err := d.list(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, obj := range items {
		if strings.HasSuffix(obj.Key, "/") {
			continue // a subdirectory (common prefix) or directory marker
		}
		files = append(files, d.unkey(obj.Key))
	}
	return files, nil
}

func (d *Disk) AllFiles(ctx context.Context, dir string) ([]string, error) {
	items, err := d.list(ctx, dir, true)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, obj := range items {
		if strings.HasSuffix(obj.Key, "/") {
			continue // a directory marker
		}
		files = append(files, d.unkey(obj.Key))
	}
	return files, nil
}

func (d *Disk) Directories(ctx context.Context, dir string) ([]string, error) {
	items, err := d.list(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, obj := range items {
		if strings.HasSuffix(obj.Key, "/") {
			dirs = append(dirs, strings.TrimSuffix(d.unkey(obj.Key), "/"))
		}
	}
	return dirs, nil
}

// AllDirectories derives every directory under dir from a single recursive
// listing, rather than one non-recursive call per level: S3 has no native
// concept of a directory tree to walk, so unlike storage/local's
// fs.WalkDir, every intermediate directory (an object's full ancestor
// chain, plus any explicit marker) has to be reconstructed from flat keys.
func (d *Disk) AllDirectories(ctx context.Context, dir string) ([]string, error) {
	items, err := d.list(ctx, dir, true)
	if err != nil {
		return nil, err
	}

	dirPrefix := strings.Trim(dir, "/")
	if dirPrefix != "" {
		dirPrefix += "/"
	}

	seen := make(map[string]struct{})
	var dirs []string
	add := func(p string) {
		if p == "" || !strings.HasPrefix(p, dirPrefix) {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		dirs = append(dirs, p)
	}

	for _, obj := range items {
		rel := d.unkey(obj.Key)
		isMarker := strings.HasSuffix(rel, "/")
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" {
			continue
		}
		segments := strings.Split(rel, "/")
		for i := 1; i < len(segments); i++ {
			add(strings.Join(segments[:i], "/"))
		}
		if isMarker {
			add(rel)
		}
	}

	sort.Strings(dirs)
	return dirs, nil
}

// list runs a single listing under dir and surfaces any listing-level
// error: ListObjectsIter reports one by yielding a single ObjectInfo{Err}
// and stopping, rather than returning it directly, since listing is
// paginated across possibly many requests.
func (d *Disk) list(ctx context.Context, dir string, recursive bool) ([]minio.ObjectInfo, error) {
	prefix := strings.TrimSuffix(d.key(dir), "/")
	if prefix != "" {
		prefix += "/"
	}

	var items []minio.ObjectInfo
	for obj := range d.client.ListObjectsIter(ctx, d.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: recursive}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage/s3: listing %s: %w", dir, obj.Err)
		}
		items = append(items, obj)
	}
	return items, nil
}

func (d *Disk) key(p string) string {
	return d.prefix + strings.TrimPrefix(p, "/")
}

func (d *Disk) unkey(key string) string {
	return strings.TrimPrefix(key, d.prefix)
}

// contentTypeFor guesses a Content-Type from p's extension, so a later
// ContentType(ctx, p) has something meaningful to report back from the
// object's stored metadata — S3 doesn't sniff content the way storage/local
// does at read time, so whatever Put doesn't set here, ContentType can
// never recover.
func contentTypeFor(p string) string {
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// isNotFound reports whether err is S3's "no such key" response.
func isNotFound(err error) bool {
	code := minio.ToErrorResponse(err).Code
	return code == minio.NoSuchKey || code == "NotFound"
}

// translateErr maps a not-found response to storage.ErrFileNotFound, so
// callers can use errors.Is regardless of which Disk backend they're
// talking to, and wraps everything else with p for context.
func translateErr(p string, err error) error {
	if isNotFound(err) {
		return storage.ErrFileNotFound
	}
	return fmt.Errorf("storage/s3: %s: %w", p, err)
}
