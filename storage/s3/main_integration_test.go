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

	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	container, err := tcminio.Run(ctx, "minio/minio:RELEASE.2024-01-16T16-07-38Z")
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

	return m.Run()
}
