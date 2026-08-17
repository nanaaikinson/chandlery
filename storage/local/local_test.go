package local_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nanaaikinson/chandlery/storage"
	"github.com/nanaaikinson/chandlery/storage/local"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	storage.TestConformance(t, func() (storage.Disk, func()) {
		d, err := local.New(t.TempDir())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d, func() {
			if err := d.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}
	})
}

func TestPutEscapesRootRejected(t *testing.T) {
	t.Parallel()

	d, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// os.Root itself is what enforces this — Put reaching a path outside
	// the disk's root would be a sandbox-escape bug, not a normal error, so
	// this pins the guarantee down rather than trusting os.Root blindly. A
	// real (non-nil) reader: Put, like io.Copy itself, isn't required to
	// tolerate a nil io.Reader, so passing one here would test that
	// unrelated contract instead of the escape rejection this test is for.
	err = d.Put(t.Context(), "../escaped.txt", strings.NewReader(""))
	if err == nil {
		t.Fatal("Put(../escaped.txt) = nil error, want a rejection")
	}
}

func TestVerifySignedURL(t *testing.T) {
	t.Parallel()

	d, err := local.New(t.TempDir(), local.WithBaseURL("https://example.com/files"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := t.Context()

	t.Run("a valid GET url verifies as GET but not PUT", func(t *testing.T) {
		t.Parallel()
		u, err := d.TemporaryUrl(ctx, "a/b.txt", time.Hour)
		if err != nil {
			t.Fatalf("TemporaryUrl: %v", err)
		}
		path, ok := d.VerifySignedURL(u, http.MethodGet)
		if !ok || path != "a/b.txt" {
			t.Fatalf("VerifySignedURL(GET) = (%q, %v), want (a/b.txt, true)", path, ok)
		}
		if _, ok := d.VerifySignedURL(u, http.MethodPut); ok {
			t.Error("a GET-signed url verified as PUT, want it rejected")
		}
	})

	t.Run("a valid PUT url verifies as PUT but not GET", func(t *testing.T) {
		t.Parallel()
		u, err := d.PresignedPutUrl(ctx, "a/b.txt", time.Hour)
		if err != nil {
			t.Fatalf("PresignedPutUrl: %v", err)
		}
		path, ok := d.VerifySignedURL(u, http.MethodPut)
		if !ok || path != "a/b.txt" {
			t.Fatalf("VerifySignedURL(PUT) = (%q, %v), want (a/b.txt, true)", path, ok)
		}
		if _, ok := d.VerifySignedURL(u, http.MethodGet); ok {
			t.Error("a PUT-signed url verified as GET, want it rejected")
		}
	})

	t.Run("an expired url is rejected", func(t *testing.T) {
		t.Parallel()
		u, err := d.TemporaryUrl(ctx, "a/b.txt", -time.Second)
		if err != nil {
			t.Fatalf("TemporaryUrl: %v", err)
		}
		if _, ok := d.VerifySignedURL(u, http.MethodGet); ok {
			t.Error("an already-expired url verified, want it rejected")
		}
	})

	t.Run("a tampered signature is rejected", func(t *testing.T) {
		t.Parallel()
		u, err := d.TemporaryUrl(ctx, "a/b.txt", time.Hour)
		if err != nil {
			t.Fatalf("TemporaryUrl: %v", err)
		}
		if _, ok := d.VerifySignedURL(u+"tampered", http.MethodGet); ok {
			t.Error("a tampered url verified, want it rejected")
		}
	})
}
