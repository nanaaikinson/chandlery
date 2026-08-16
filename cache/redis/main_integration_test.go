//go:build integration

// Package redis_test's integration suite runs against a single real Redis
// container (via testcontainers-go), started once for the test binary.
// Run with: go test -tags=integration ./cache/redis/...
package redis_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting redis container:", err)
		return 1
	}
	defer container.Terminate(ctx)

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting connection string:", err)
		return 1
	}

	// os.Setenv, not t.Setenv: this exercises redis.New's real entry point
	// (REDIS_URL) before any test's t.Parallel runs, same as
	// db/postgres/main_integration_test.go does for DB_URL.
	os.Setenv("REDIS_URL", connStr)

	return m.Run()
}
