// Package postgres builds a *bun.DB connection to Postgres.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

// Config is how to reach Postgres and size the pool. ConfigFromEnv reads it
// from the environment, which is what New does; a service that owns its
// configuration builds one itself and passes it to Open.
type Config struct {
	// DSN is the connection URL, e.g.
	// postgres://user:pass@host:5432/db?sslmode=disable.
	DSN string
	// MaxOpenConns caps open connections; zero or less means no cap. The
	// idle pool keeps the same number (ten when there is no cap), since a
	// non-positive SetMaxIdleConns would retain no idle connections at all.
	MaxOpenConns int
	// ConnMaxLifetime and ConnMaxIdleTime retire connections; zero keeps
	// them forever.
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	// Debug logs every query with its arguments (bundebug). Never in
	// production.
	Debug bool
}

// ConfigFromEnv reads DB_URL (see DSN for its local default),
// DB_MAX_OPEN_CONNS (10), DB_CONN_MAX_LIFETIME_MINUTES (30),
// DB_CONN_MAX_IDLE_TIME_MINUTES (5) and DB_DEBUG (false).
func ConfigFromEnv() Config {
	return Config{
		DSN:             DSN(),
		MaxOpenConns:    env.GetInt("DB_MAX_OPEN_CONNS", 10),
		ConnMaxLifetime: time.Duration(env.GetInt("DB_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute,
		ConnMaxIdleTime: time.Duration(env.GetInt("DB_CONN_MAX_IDLE_TIME_MINUTES", 5)) * time.Minute,
		Debug:           env.GetBool("DB_DEBUG", false),
	}
}

// LogValue keeps the DSN's password out of a log line that includes the
// Config.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("dsn", redactDSN(c.DSN)),
		slog.Int("max_open_conns", c.MaxOpenConns),
		slog.Duration("conn_max_lifetime", c.ConnMaxLifetime),
		slog.Duration("conn_max_idle_time", c.ConnMaxIdleTime),
		slog.Bool("debug", c.Debug),
	)
}

// Open opens a *bun.DB with cfg using bun's own pgdriver (a lightweight,
// pure-Go Postgres driver maintained alongside bun). It does no network I/O:
// call Ping to check the database is reachable.
//
// Unlike New it returns an error instead of panicking, and the error never
// contains the DSN: pgdriver's parse errors quote it whole, password
// included, and a startup error goes straight to a log.
func Open(cfg Config) (*bun.DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("db/postgres: empty DSN")
	}
	connector, err := newConnector(cfg.DSN)
	if err != nil {
		return nil, err
	}
	sqldb := sql.OpenDB(connector)

	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	if cfg.MaxOpenConns > 0 {
		sqldb.SetMaxIdleConns(cfg.MaxOpenConns)
	} else {
		sqldb.SetMaxIdleConns(10)
	}
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	bdb := bun.NewDB(sqldb, pgdialect.New())

	registrarsMu.Lock()
	registrars := append([]func(*bun.DB){}, modelRegistrars...)
	registrarsMu.Unlock()
	for _, register := range registrars {
		register(bdb)
	}

	if cfg.Debug {
		bdb.AddQueryHook(bundebug.NewQueryHook())
	}

	return bdb, nil
}

// New opens a *bun.DB with ConfigFromEnv, panicking if DB_URL can't be
// parsed. Prefer Open in a service that loads its own configuration.
func New() *bun.DB {
	bdb, err := Open(ConfigFromEnv())
	if err != nil {
		panic(err)
	}
	return bdb
}

// newConnector builds pgdriver's connector, turning the panic pgdriver
// raises for a DSN it can't parse into an error that names the DSN only with
// its password redacted.
func newConnector(dsn string) (connector *pgdriver.Connector, err error) {
	defer func() {
		if r := recover(); r != nil {
			connector = nil
			err = fmt.Errorf("db/postgres: invalid DSN %s", redactDSN(dsn))
		}
	}()
	return pgdriver.NewConnector(pgdriver.WithDSN(dsn)), nil
}

// redactDSN names where dsn points (scheme, user, host and database) and
// nothing else: no password, and no query string, which libpq-style DSNs use
// for password= too. Anything without a scheme and host is a placeholder,
// since an unparseable string is no safer to print whole.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "(unparseable)"
	}
	out := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if u.User != nil {
		out.User = url.User(u.User.Username())
	}
	return out.String()
}

// Ping verifies connectivity to the database.
func Ping(ctx context.Context, db *bun.DB) error {
	return db.PingContext(ctx)
}
