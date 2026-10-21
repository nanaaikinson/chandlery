//go:build integration

package s3_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/nanaaikinson/chandlery/storage"
	"github.com/nanaaikinson/chandlery/storage/s3"
)

// A Disk built by NewDisk from a caller's Config meets the same storage
// contract as one built by New from the environment.
func TestNewDiskConformance(t *testing.T) {
	t.Parallel()
	cfg := s3.Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
	}
	setup, err := s3.NewDisk(cfg, bucket)
	if err != nil {
		t.Fatalf("NewDisk: %v", err)
	}
	if err := setup.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	storage.TestConformance(t, func() (storage.Disk, func()) {
		prefix := fmt.Sprintf("newdisk-%d/", prefixCounter.Add(1))
		d, err := s3.NewDisk(cfg, bucket, s3.WithPrefix(prefix))
		if err != nil {
			t.Fatalf("NewDisk: %v", err)
		}
		return d, func() {}
	})
}
