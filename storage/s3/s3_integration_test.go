//go:build integration

package s3_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/nanaaikinson/chandlery/storage"
	"github.com/nanaaikinson/chandlery/storage/s3"
)

const bucket = "conformance"

// prefixCounter gives every factory call its own namespace, so
// storage.TestConformance's subtests can run in parallel against the one
// shared bucket without their keys colliding — mirrors
// cache/redis/redis_integration_test.go's own prefixCounter.
var prefixCounter atomic.Int64

func TestConformance(t *testing.T) {
	t.Parallel()

	setupDisk, err := s3.New(bucket)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := setupDisk.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	storage.TestConformance(t, func() (storage.Disk, func()) {
		prefix := fmt.Sprintf("conformance-%d/", prefixCounter.Add(1))
		d, err := s3.New(bucket, s3.WithPrefix(prefix))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d, func() {}
	})
}

func TestDeleteDirectoryRequiresPrefix(t *testing.T) {
	t.Parallel()

	d, err := s3.New(bucket)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.DeleteDirectory(context.Background(), "anything"); !errors.Is(err, storage.ErrNoPrefix) {
		t.Fatalf("DeleteDirectory (no prefix) = %v, want %v", err, storage.ErrNoPrefix)
	}
}
