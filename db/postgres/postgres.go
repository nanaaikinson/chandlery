// Package postgres builds a *bun.DB connection to Postgres.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nanaaikinson/chandlery/env"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
)

// modelRegistrars are run against every *bun.DB built by New or Open. Callback-based
// so models register themselves from their own init() instead of db
// importing internal/models directly, which would create an import cycle.
// Guarded by registrarsMu since the intended usage (registering from init())
// is single-threaded, but nothing enforces that a caller won't register from
// a goroutine or call New() concurrently with a registration.
var (
	registrarsMu    sync.Mutex
	modelRegistrars []func(*bun.DB)
)

// RegisterModelFunc queues fn to run against every *bun.DB built by New or Open —
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
	// Debug logs every failed query with its arguments to stderr
	// (bundebug, not verbose). Never in production.
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

// A Config never shows its DSN's password, however it is printed: through
// slog (LogValue), fmt with any verb (Format), or JSON, including slog's JSON
// handler, when it is a field of something larger (MarshalJSON). Each shows
// the redacted form.

// redacted is what a Config prints as.
type redacted struct {
	DSN             string        `json:"dsn"`
	MaxOpenConns    int           `json:"max_open_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `json:"conn_max_idle_time"`
	Debug           bool          `json:"debug"`
}

func (c Config) redacted() redacted {
	return redacted{redactDSN(c.DSN), c.MaxOpenConns, c.ConnMaxLifetime, c.ConnMaxIdleTime, c.Debug}
}

// LogValue implements slog.LogValuer.
func (c Config) LogValue() slog.Value {
	r := c.redacted()
	return slog.GroupValue(
		slog.String("dsn", r.DSN),
		slog.Int("max_open_conns", r.MaxOpenConns),
		slog.Duration("conn_max_lifetime", r.ConnMaxLifetime),
		slog.Duration("conn_max_idle_time", r.ConnMaxIdleTime),
		slog.Bool("debug", r.Debug),
	)
}

// Format implements fmt.Formatter, for every verb.
func (c Config) Format(f fmt.State, _ rune) {
	r := c.redacted()
	fmt.Fprintf(f, "{DSN:%s MaxOpenConns:%d ConnMaxLifetime:%s ConnMaxIdleTime:%s Debug:%t}",
		r.DSN, r.MaxOpenConns, r.ConnMaxLifetime, r.ConnMaxIdleTime, r.Debug)
}

// MarshalJSON implements json.Marshaler.
func (c Config) MarshalJSON() ([]byte, error) { return json.Marshal(c.redacted()) }

// Open opens a *bun.DB with cfg using bun's own pgdriver (a lightweight,
// pure-Go Postgres driver maintained alongside bun). It does no network I/O:
// call Ping to check the database is reachable.
//
// pgdriver still fills what the DSN leaves out from PGHOST, PGPORT, PGUSER
// and PGDATABASE, so a DSN that names its host, user and database is the
// whole story.
//
// Unlike New it returns an error instead of panicking, and the error never
// contains the DSN: pgdriver's parse errors quote it whole, password
// included, and a startup error goes straight to a log.
func Open(cfg Config) (*bun.DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("db/postgres: empty DSN")
	}
	if u, err := url.Parse(cfg.DSN); err == nil && ambiguousDSN(cfg.DSN, u) {
		// Connecting would dial a host made of part of the password, and the
		// dial error would print it.
		return nil, errors.New("db/postgres: DSN is ambiguous: percent-encode the password")
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
// raises for a DSN it rejects into an error. The error names the DSN with its
// password redacted, and keeps pgdriver's reason ("sslmode 'requre' is not
// supported") unless that reason could carry part of the DSN: a URL parse
// error quotes its input, so it is only "not a valid URL". A runtime error is
// a bug in pgdriver, not a bad DSN, so it panics on.
func newConnector(dsn string) (connector *pgdriver.Connector, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if re, ok := r.(runtime.Error); ok {
			panic(re)
		}
		connector = nil
		err = fmt.Errorf("db/postgres: invalid DSN %s: %s", redactDSN(dsn), dsnRejection(dsn, r))
	}()
	return pgdriver.NewConnector(pgdriver.WithDSN(dsn)), nil
}

// dsnRejection is pgdriver's reason for rejecting dsn, when it is safe to
// print.
func dsnRejection(dsn string, r any) string {
	if e, ok := r.(error); ok {
		var urlErr *url.Error
		if errors.As(e, &urlErr) {
			return "not a valid URL"
		}
	}
	msg := fmt.Sprint(r)
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" && strings.Contains(msg, pw) {
			return "rejected by the driver"
		}
	}
	if strings.Contains(msg, dsn) || strings.Contains(msg, "@") {
		return "rejected by the driver"
	}
	return msg
}

// redactDSN names where dsn points (scheme, user, host and database) and
// nothing else: no password, and no query string, which libpq-style DSNs use
// for password= too. Anything it can't read with confidence is a
// placeholder, since a half-read string is no safer to print. In particular
// a password that isn't percent-encoded and holds a "/", "#" or "?" makes
// url.Parse misplace it into the host, path or fragment; the "@" left behind
// there, or a userinfo-less URL that still has one, gives it away.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	const unreadable = "(unparseable)"
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return unreadable
	}
	if ambiguousDSN(dsn, u) {
		return unreadable
	}
	if u.Host == "" && u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return unreadable
	}
	// A unix-socket DSN has no host; its host= query parameter isn't shown
	// with the rest of the query string.
	out := &url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	if u.User != nil {
		out.User = url.User(u.User.Username())
	}
	return out.String()
}

// ambiguousDSN reports whether url.Parse may have misread dsn: a password
// that isn't percent-encoded and holds "/", "#" or "?" ends the userinfo
// early, leaving part of it in the host, path, query or fragment, with an
// "@" there or a userinfo-less URL that still has one. A Postgres DSN never
// has a fragment.
func ambiguousDSN(dsn string, u *url.URL) bool {
	return u.Fragment != "" ||
		u.User == nil && strings.Contains(dsn, "@") ||
		strings.Contains(u.Host, "@") || strings.Contains(u.Path, "@") ||
		strings.Contains(u.RawQuery, "@") ||
		strings.HasSuffix(u.Host, ":")
}

// Ping verifies connectivity to the database.
func Ping(ctx context.Context, db *bun.DB) error {
	return db.PingContext(ctx)
}
