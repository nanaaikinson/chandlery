//go:build integration

// Package s3_test's integration suite runs against a single real MinIO
// container (via testcontainers-go), started once for the test binary —
// MinIO is just the available S3-compatible backend to test against here,
// not the thing under test. Run with: go test -tags=integration ./storage/s3/...
package s3_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/nanaaikinson/chandlery/storage/s3"
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	// quay.io, not Docker Hub: MinIO withdrew the minio/minio repository
	// from Docker Hub, so the old pin stopped resolving — "pull access
	// denied ... repository does not exist" — for everyone at once, with no
	// change on our side. quay.io is where MinIO publishes now, and it
	// carries this same release, so only the registry changed.
	container, err := tcminio.Run(ctx, "quay.io/minio/minio:RELEASE.2024-01-16T16-07-38Z")
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting minio container:", err)
		return 1
	}
	defer container.Terminate(ctx)

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting connection string:", err)
		return 1
	}

	// os.Setenv, not t.Setenv: this exercises s3.New's real entry point
	// (S3_ENDPOINT/S3_ACCESS_KEY_ID/S3_SECRET_ACCESS_KEY) before any test's
	// t.Parallel runs, same as db/postgres and cache/redis's own
	// main_integration_test.go do for their connection env vars. The
	// container's credentials match s3.AccessKeyID/SecretAccessKey's own
	// defaults, so nothing further needs setting for those.
	os.Setenv("S3_ENDPOINT", connStr)

	if err := waitForS3(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "waiting for minio:", err)
		return 1
	}

	return m.Run()
}

// waitForS3 blocks until MinIO will actually serve an S3 request.
//
// The container module's wait strategy is an HTTP liveness probe, which goes
// green when the server binds its port — before the object layer is up. A
// request in that window comes back "Server not initialized yet", which is
// how this suite failed on CI while passing locally: the gap is short enough
// to lose on a warm machine and wide enough to hit on a loaded runner.
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
