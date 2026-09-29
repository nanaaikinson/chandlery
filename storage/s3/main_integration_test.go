//go:build integration

// Package s3_test's integration suite runs against a single real RustFS
// container (via testcontainers-go), started once for the test binary —
// RustFS is just the available S3-compatible backend to test against here,
// not the thing under test. Run with: go test -tags=integration ./storage/s3/...
package s3_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nanaaikinson/chandlery/storage/s3"
)

const (
	s3Port    = "9000/tcp"
	accessKey = "chandlery-test"
	secretKey = "chandlery-test-secret"
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	// RustFS, not MinIO: MinIO withdrew minio/minio from Docker Hub and
	// then locked quay.io/minio/minio behind auth ("unauthorized: access to
	// the requested resource is not authorized"), so every MinIO pin stopped
	// resolving with no change on our side. RustFS is an independent
	// S3-compatible server with an official image, pinned to a stable
	// release here, never latest. There is no testcontainers module for it,
	// hence the generic container.
	container, err := testcontainers.Run(ctx, "rustfs/rustfs:1.0.0",
		testcontainers.WithExposedPorts(s3Port),
		testcontainers.WithEnv(map[string]string{
			"RUSTFS_ACCESS_KEY": accessKey,
			"RUSTFS_SECRET_KEY": secretKey,
		}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort(s3Port)),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting rustfs container:", err)
		return 1
	}
	defer container.Terminate(ctx)

	endpoint, err := container.PortEndpoint(ctx, s3Port, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting endpoint:", err)
		return 1
	}

	// os.Setenv, not t.Setenv: this exercises s3.New's real entry point
	// (S3_ENDPOINT/S3_ACCESS_KEY_ID/S3_SECRET_ACCESS_KEY) before any test's
	// t.Parallel runs, same as db/postgres and cache/redis's own
	// main_integration_test.go do for their connection env vars. The
	// credentials are set explicitly rather than leaning on s3's
	// minioadmin defaults, since RustFS's own default root credential
	// differs.
	os.Setenv("S3_ENDPOINT", endpoint)
	os.Setenv("S3_ACCESS_KEY_ID", accessKey)
	os.Setenv("S3_SECRET_ACCESS_KEY", secretKey)

	if err := waitForS3(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "waiting for rustfs:", err)
		return 1
	}

	return m.Run()
}

// waitForS3 blocks until the server will actually serve an S3 request.
//
// The container's wait strategy only checks the port is listening, which goes
// green when the server binds it — before the object layer is up. (MinIO
// answered in that window with "Server not initialized yet", which is how
// this suite failed on CI while passing locally: the gap is short enough to
// lose on a warm machine and wide enough to hit on a loaded runner.)
//
// Polling the real client is the only honest readiness signal here, since it
// asks the exact question the tests are about to. Sleeping for a real
// external service's own startup is the same exception cache/redis makes for
// Redis's TTL clock — there is nothing here to fast-forward.
func waitForS3(ctx context.Context) error {
	disk, err := s3.New(bucket)
	if err != nil {
		return fmt.Errorf("building a disk: %w", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for attempt := 1; ; attempt++ {
		err = disk.EnsureBucket(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not serving after %d attempts: %w", attempt, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
