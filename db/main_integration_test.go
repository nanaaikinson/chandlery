//go:build integration

// Package db_test's integration suite exercises the dialect-agnostic
// Repository[T]/Model against a real Postgres container (via
// testcontainers-go, started once for the test binary) — Postgres is just
// the available backend to test against here, not the thing under test;
// Postgres-specific behavior has its own suite in db/postgres. Run with:
// go test -tags=integration ./db/...
package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

var testDB *bun.DB

const schema = `
	create table items (
		id text primary key,
		name text not null,
		price int not null,
		created_at timestamptz not null default current_timestamp,
		updated_at timestamptz not null default current_timestamp
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

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	defer sqldb.Close()

	testDB = bun.NewDB(sqldb, pgdialect.New())
	defer testDB.Close()

	if _, err := testDB.ExecContext(ctx, schema); err != nil {
		fmt.Fprintln(os.Stderr, "applying schema:", err)
		return 1
	}

	return m.Run()
}
