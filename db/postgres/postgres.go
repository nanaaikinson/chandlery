// Package postgres builds a *bun.DB connection to Postgres.
package postgres

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/nanaaikinson/chandlery/env"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
)

// modelRegistrars are run against every *bun.DB built by New. Callback-based
// so models register themselves from their own init() instead of db
// importing internal/models directly, which would create an import cycle.
// Guarded by registrarsMu since the intended usage (registering from init())
// is single-threaded, but nothing enforces that a caller won't register from
// a goroutine or call New() concurrently with a registration.
var (
	registrarsMu    sync.Mutex
	modelRegistrars []func(*bun.DB)
)

// RegisterModelFunc queues fn to run against every *bun.DB built by New —
// bun requires models used only via m2m relations to be registered explicitly.
func RegisterModelFunc(fn func(*bun.DB)) {
	registrarsMu.Lock()
	defer registrarsMu.Unlock()
	modelRegistrars = append(modelRegistrars, fn)
}

// DSN returns the Postgres connection string from DB_URL, falling back to
// a local default both when unset and when present-but-blank (as it might
// ship in a checked-in .env.example), since a blank DSN can never be a
// deliberate value.
func DSN() string {
	dsn := env.Get("DB_URL", "")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
	}
	return dsn
}

// New opens a *bun.DB connection to Postgres using bun's own pgdriver
// (a lightweight, pure-Go Postgres driver maintained alongside bun).
func New() *bun.DB {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(DSN())))

	// Pool sizing. database/sql treats a non-positive SetMaxOpenConns as
	// "unlimited", but a non-positive SetMaxIdleConns means "no idle
	// connections retained" — so a DB_MAX_OPEN_CONNS of 0 (read by an
	// operator as "unlimited") must not also zero out idle pooling.
	maxOpenConns := env.GetInt("DB_MAX_OPEN_CONNS", 10)
	sqldb.SetMaxOpenConns(maxOpenConns)
	if maxOpenConns > 0 {
		sqldb.SetMaxIdleConns(maxOpenConns)
	} else {
		sqldb.SetMaxIdleConns(10)
	}
	sqldb.SetConnMaxLifetime(time.Duration(env.GetInt("DB_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute)
	sqldb.SetConnMaxIdleTime(time.Duration(env.GetInt("DB_CONN_MAX_IDLE_TIME_MINUTES", 5)) * time.Minute)

	bdb := bun.NewDB(sqldb, pgdialect.New())

	registrarsMu.Lock()
	registrars := append([]func(*bun.DB){}, modelRegistrars...)
	registrarsMu.Unlock()
	for _, register := range registrars {
		register(bdb)
	}

	if env.GetBool("DB_DEBUG", false) {
		bdb.AddQueryHook(bundebug.NewQueryHook())
	}

	return bdb
}

// Ping verifies connectivity to the database.
func Ping(ctx context.Context, db *bun.DB) error {
	return db.PingContext(ctx)
}
