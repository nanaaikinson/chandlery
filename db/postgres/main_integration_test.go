//go:build integration

// Package postgres_test's integration suite runs against a single real
// Postgres container (via testcontainers-go), started once for the test
// binary. Run with: go test -tags=integration ./db/postgres/...
package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/uptrace/bun"

	chandlerypg "github.com/nanaaikinson/chandlery/db/postgres"
)

var testDB *bun.DB

const schema = `
	create table accounts (
		id text primary key,
		email text not null unique
	);

	create table categories (
		id text primary key
	);

	create table products (
		id text primary key,
		category_id text not null references categories(id) on delete restrict
	);
`

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("chandlery_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting postgres container:", err)
		return 1
	}
	defer container.Terminate(ctx)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "getting connection string:", err)
		return 1
	}

	// Exercises DSN()/New() themselves (DB_URL is their real entry point),
	// rather than hand-rolling a parallel bun.DB just for this test binary.
	os.Setenv("DB_URL", dsn)
	testDB = chandlerypg.New()
	defer testDB.Close()

	if _, err := testDB.ExecContext(ctx, schema); err != nil {
		fmt.Fprintln(os.Stderr, "applying schema:", err)
		return 1
	}

	return m.Run()
}

func TestNewAndPing(t *testing.T) {
	t.Parallel()

	if err := chandlerypg.Ping(t.Context(), testDB); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
