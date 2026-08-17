// Package local implements storage.Disk on the local filesystem, sandboxed
// to a root directory via os.Root so a caller-supplied path (e.g. from an
// upload request) can never read or write outside it, symlinks included.
package local

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nanaaikinson/chandlery/storage"
)

// Disk is a storage.Disk backed by a directory tree on the local
// filesystem, rooted via os.Root so every path this Disk touches is
// confined to that tree regardless of "..": os.Root's own guarantee
// (Go 1.24+) is stronger than the classic filepath.Clean-and-check trick —
// it also refuses a symlink that points outside the root.
type Disk struct {
	root *os.Root

	baseURL     string
	baseURLPath string // the path component of baseURL, cached once rather than re-parsed on every VerifySignedURL call

	signingKey []byte
}

var _ storage.Disk = (*Disk)(nil)

// Option configures a Disk.
type Option func(*Disk)

// WithBaseURL sets the base Url/TemporaryUrl/PresignedPutUrl build on top
// of (e.g. "https://cdn.example.com/uploads"). Defaults to "", producing
// root-relative URLs like "/path/to/file".
func WithBaseURL(base string) Option {
	return func(d *Disk) {
		d.baseURL = strings.TrimSuffix(base, "/")
		if u, err := url.Parse(d.baseURL); err == nil {
			d.baseURLPath = u.Path
		}
	}
}

// WithSigningKey sets the HMAC key TemporaryUrl/PresignedPutUrl sign with
// and VerifySignedURL checks against. Required for a signature minted by
// one process to verify in another (a second instance, or a later restart
// of this one) — without it, New generates a random key that's only ever
// valid for this process's own lifetime.
func WithSigningKey(key []byte) Option {
	return func(d *Disk) { d.signingKey = key }
}

// New opens a Disk rooted at root, creating it if it doesn't already exist.
func New(root string, opts ...Option) (*Disk, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("storage/local: creating root %s: %w", root, err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("storage/local: opening root %s: %w", root, err)
	}

	d := &Disk{root: r}
	for _, opt := range opts {
		opt(d)
	}
	if d.signingKey == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			r.Close()
			return nil, fmt.Errorf("storage/local: generating signing key: %w", err)
		}
		d.signingKey = key
	}
	return d, nil
}

// Close releases the root directory handle os.Root holds open.
func (d *Disk) Close() error {
	return d.root.Close()
}

func (d *Disk) Put(ctx context.Context, p string, contents io.Reader) error {
	p = normalize(p)
	if err := d.ensureParentDir(p); err != nil {
		return err
	}
	f, err := d.root.Create(p)
	if err != nil {
		return fmt.Errorf("storage/local: creating %s: %w", p, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, contents); err != nil {
		return fmt.Errorf("storage/local: writing %s: %w", p, err)
	}
	return nil
}

func (d *Disk) PutString(ctx context.Context, p string, contents string) error {
	return d.Put(ctx, p, strings.NewReader(contents))
}

func (d *Disk) Get(ctx context.Context, p string) ([]byte, error) {
	data, err := d.root.ReadFile(normalize(p))
	if err != nil {
		return nil, translateErr(p, err)
	}
	return data, nil
}

func (d *Disk) Exists(ctx context.Context, p string) (bool, error) {
	_, err := d.root.Stat(normalize(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("storage/local: checking %s: %w", p, err)
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
		// Removing an absent path isn't an error, same convention as
		// cache.Store.Forget.
		if err := d.root.Remove(normalize(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("storage/local: %w", errors.Join(errs...))
	}
	return nil
}

func (d *Disk) Copy(ctx context.Context, from, to string) error {
	from, to = normalize(from), normalize(to)

	src, err := d.root.Open(from)
	if err != nil {
		return translateErr(from, err)
	}
	defer src.Close()

	if err := d.ensureParentDir(to); err != nil {
		return err
	}
	dst, err := d.root.Create(to)
	if err != nil {
		return fmt.Errorf("storage/local: creating %s: %w", to, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("storage/local: copying %s to %s: %w", from, to, err)
	}
	return nil
}

func (d *Disk) Move(ctx context.Context, from, to string) error {
	from, to = normalize(from), normalize(to)

	if err := d.ensureParentDir(to); err != nil {
		return err
	}
	if err := d.root.Rename(from, to); err != nil {
		return translateErr(from, err)
	}
	return nil
}

func (d *Disk) Size(ctx context.Context, p string) (int64, error) {
	info, err := d.root.Stat(normalize(p))
	if err != nil {
		return 0, translateErr(p, err)
	}
	return info.Size(), nil
}

func (d *Disk) LastModified(ctx context.Context, p string) (time.Time, error) {
	info, err := d.root.Stat(normalize(p))
	if err != nil {
		return time.Time{}, translateErr(p, err)
	}
	return info.ModTime(), nil
}

// ContentType sniffs the file's first 512 bytes: unlike storage/s3 (which
// reports back whatever Content-Type Put stored as object metadata), plain
// local files have no metadata slot to have stored one in.
func (d *Disk) ContentType(ctx context.Context, p string) (string, error) {
	f, err := d.root.Open(normalize(p))
	if err != nil {
		return "", translateErr(p, err)
	}
	defer f.Close()

	// io.ReadFull, not a single Read call: Read is allowed to return fewer
	// bytes than the buffer even mid-stream (not just at EOF) — on a plain
	// local file that's academic, but nothing here is specific to a plain
	// file once opened through fs.FS, so don't assume a short read means
	// "that's everything there is."
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("storage/local: reading %s: %w", p, err)
	}
	return http.DetectContentType(buf[:n]), nil
}

// Url builds a root-relative (or, with WithBaseURL, absolute) URL. It's
// just string construction — nothing here actually serves the file; that's
// up to whatever this Disk is embedded in.
func (d *Disk) Url(p string) string {
	return d.baseURL + "/" + normalize(filepath.ToSlash(p))
}

// TemporaryUrl mints an HMAC-signed GET URL: local disk has no native
// signed-URL mechanism the way S3 does, so this signs Url(p) with an
// expiry instead. Nothing in this package serves the file at the URL it
// returns — pair this with VerifySignedURL in whatever handler does.
func (d *Disk) TemporaryUrl(ctx context.Context, p string, expiration time.Duration) (string, error) {
	return d.signedURL(p, http.MethodGet, expiration), nil
}

// PresignedPutUrl mints an HMAC-signed PUT URL, the local-disk equivalent
// of S3's SigV4-signed upload URL. Same caveat as TemporaryUrl: this only
// mints the URL, it doesn't serve an endpoint that accepts the upload.
func (d *Disk) PresignedPutUrl(ctx context.Context, p string, expiration time.Duration) (string, error) {
	return d.signedURL(p, http.MethodPut, expiration), nil
}

// VerifySignedURL reports whether rawURL carries a still-valid signature
// for method (http.MethodGet for a TemporaryUrl, http.MethodPut for a
// PresignedPutUrl) — method must match what the URL was minted for, so a
// GET link can't be replayed as a PUT. Returns the disk-relative path the
// signature covers.
func (d *Disk) VerifySignedURL(rawURL, method string) (p string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}

	relPath := strings.TrimPrefix(u.Path, d.baseURLPath)
	relPath = strings.TrimPrefix(relPath, "/")

	q := u.Query()
	expiresParam, sig := q.Get("expires"), q.Get("signature")
	if expiresParam == "" || sig == "" {
		return "", false
	}
	expiresUnix, err := strconv.ParseInt(expiresParam, 10, 64)
	if err != nil {
		return "", false
	}
	expires := time.Unix(expiresUnix, 0)
	if time.Now().After(expires) {
		return "", false
	}

	want := d.sign(relPath, method, expires)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return "", false
	}
	return relPath, true
}

func (d *Disk) signedURL(p, method string, expiration time.Duration) string {
	expires := time.Now().Add(expiration)
	sig := d.sign(p, method, expires)
	return fmt.Sprintf("%s?expires=%d&signature=%s", d.Url(p), expires.Unix(), sig)
}

func (d *Disk) sign(p, method string, expires time.Time) string {
	mac := hmac.New(sha256.New, d.signingKey)
	fmt.Fprintf(mac, "%s:%s:%d", method, p, expires.Unix())
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (d *Disk) MakeDirectory(ctx context.Context, p string) error {
	if err := d.root.MkdirAll(normalize(p), 0o755); err != nil {
		return fmt.Errorf("storage/local: making directory %s: %w", p, err)
	}
	return nil
}

func (d *Disk) DeleteDirectory(ctx context.Context, p string) error {
	if err := d.root.RemoveAll(normalize(p)); err != nil {
		return fmt.Errorf("storage/local: deleting directory %s: %w", p, err)
	}
	return nil
}

func (d *Disk) Files(ctx context.Context, dir string) ([]string, error) {
	return d.listShallow(dir, false)
}

func (d *Disk) AllFiles(ctx context.Context, dir string) ([]string, error) {
	return d.listDeep(dir, false)
}

func (d *Disk) Directories(ctx context.Context, dir string) ([]string, error) {
	return d.listShallow(dir, true)
}

func (d *Disk) AllDirectories(ctx context.Context, dir string) ([]string, error) {
	return d.listDeep(dir, true)
}

// listShallow lists dir's immediate children (files if wantDirs is false,
// subdirectories if true). A missing dir is reported as no results, not an
// error — absence isn't automatically an error anywhere else in this
// library either (e.g. cache.Store.Get on a missing key).
func (d *Disk) listShallow(dir string, wantDirs bool) ([]string, error) {
	entries, err := fs.ReadDir(d.root.FS(), toFSPath(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage/local: listing %s: %w", dir, err)
	}
	var results []string
	for _, e := range entries {
		if e.IsDir() == wantDirs {
			results = append(results, path.Join(dir, e.Name()))
		}
	}
	return results, nil
}

// listDeep is listShallow's recursive counterpart, walking every descendant
// of dir rather than just its immediate children.
func (d *Disk) listDeep(dir string, wantDirs bool) ([]string, error) {
	root := toFSPath(dir)
	var results []string
	err := fs.WalkDir(d.root.FS(), root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// entry.IsDir() == wantDirs already excludes the root directory
		// itself when wantDirs is false (root is always a dir); the p !=
		// root check is what excludes it when wantDirs is true, so a
		// directory never lists itself as its own subdirectory.
		if entry.IsDir() == wantDirs && p != root {
			results = append(results, p)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage/local: listing %s: %w", dir, err)
	}
	return results, nil
}

// ensureParentDir creates p's parent directory (and any of its own
// ancestors), if it has one, so Put/Copy's target doesn't have to already
// exist — mirroring S3, where a key like "a/b/c.txt" needs no directory to
// pre-exist at all. p must already be normalize()d.
func (d *Disk) ensureParentDir(p string) error {
	dir := path.Dir(filepath.ToSlash(p))
	if dir == "." {
		return nil
	}
	if err := d.root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage/local: creating %s: %w", dir, err)
	}
	return nil
}

// normalize strips a leading "/": os.Root treats an absolute path as
// escaping its root and rejects it outright, but storage.Store's contract
// is that "/reports/jan.csv" and "reports/jan.csv" name the same file
// (storage/s3 already treats them the same way, via its own key()) — a
// caller shouldn't get a different answer only because it happened to
// spell a path with a leading slash.
func normalize(p string) string {
	return strings.TrimPrefix(p, "/")
}

// toFSPath adapts a storage path to fs.FS's convention that the root
// directory is named "." rather than "" or "/".
func toFSPath(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return "."
	}
	return p
}

// translateErr maps a not-found error to storage.ErrFileNotFound, so
// callers can use errors.Is regardless of which Disk backend they're
// talking to, and wraps everything else with p for context.
func translateErr(p string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return storage.ErrFileNotFound
	}
	return fmt.Errorf("storage/local: %s: %w", p, err)
}
