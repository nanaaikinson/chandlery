package postgres_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	chandlerypg "github.com/nanaaikinson/chandlery/db/postgres"
)

const secret = "s3cretpw"

// ConfigFromEnv keeps New's defaults when nothing is set. Not parallel: it
// sets environment variables.
func TestConfigFromEnvDefaults(t *testing.T) {
	for _, name := range []string{"DB_URL", "DB_MAX_OPEN_CONNS", "DB_CONN_MAX_LIFETIME_MINUTES", "DB_CONN_MAX_IDLE_TIME_MINUTES", "DB_DEBUG"} {
		t.Setenv(name, "")
	}
	got := chandlerypg.ConfigFromEnv()
	want := chandlerypg.Config{
		DSN:             "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
		MaxOpenConns:    10,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
	if got != want {
		t.Fatalf("ConfigFromEnv() = %+v, want %+v", got, want)
	}
}

// Not parallel: it sets environment variables.
func TestConfigFromEnvReadsEveryVariable(t *testing.T) {
	t.Setenv("DB_URL", "postgres://app@db:5432/app")
	t.Setenv("DB_MAX_OPEN_CONNS", "25")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "60")
	t.Setenv("DB_CONN_MAX_IDLE_TIME_MINUTES", "2")
	t.Setenv("DB_DEBUG", "true")
	got := chandlerypg.ConfigFromEnv()
	want := chandlerypg.Config{
		DSN:             "postgres://app@db:5432/app",
		MaxOpenConns:    25,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 2 * time.Minute,
		Debug:           true,
	}
	if got != want {
		t.Fatalf("ConfigFromEnv() = %+v, want %+v", got, want)
	}
}

func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	t.Parallel()
	if db, err := chandlerypg.Open(chandlerypg.Config{}); err == nil {
		db.Close()
		t.Fatal("Open with no DSN succeeded")
	}
}

// A DSN pgdriver can't parse is an error, not a panic, and the error never
// carries the password: it goes straight to a startup log.
func TestOpenInvalidDSNIsAnErrorWithoutThePassword(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{
		"postgres://app:" + secret + "@db.example:5432/wishboard\x7f?sslmode=require",
		"postgres://app:" + secret + "@db.example:notaport/wishboard",
		"not a url " + secret,
		"postgres://app@db.example:5432/wishboard?password=" + secret + "&sslmode=bogus\x7f",
	} {
		db, err := chandlerypg.Open(chandlerypg.Config{DSN: dsn})
		if err == nil {
			db.Close()
			t.Errorf("Open(%q) succeeded", dsn)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("Open(%q) error quotes the password: %v", dsn, err)
		}
	}
}

// Open sizes the pool from the Config, without touching the network.
func TestOpenSizesThePool(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ maxOpen, wantMaxOpen int }{
		{7, 7},
		{0, 0}, // database/sql reports 0 for no cap
	} {
		db, err := chandlerypg.Open(chandlerypg.Config{DSN: "postgres://app@127.0.0.1:1/app", MaxOpenConns: tc.maxOpen})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got := db.DB.Stats().MaxOpenConnections; got != tc.wantMaxOpen {
			t.Errorf("MaxOpenConns %d: pool max = %d, want %d", tc.maxOpen, got, tc.wantMaxOpen)
		}
		db.Close()
	}
}

// New keeps panicking on a bad DB_URL, but its message no longer carries the
// password. Not parallel: it sets environment variables.
func TestNewPanicsWithoutThePassword(t *testing.T) {
	t.Setenv("DB_URL", "postgres://app:"+secret+"@db.example:5432/wishboard\x7f")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("New with a malformed DB_URL didn't panic")
		}
		if msg := fmt.Sprint(r); strings.Contains(msg, secret) {
			t.Fatalf("panic quotes the password: %s", msg)
		}
	}()
	chandlerypg.New()
}

// Logging a Config shows where it points, never the password.
func TestConfigLogValueRedactsThePassword(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("db", "config",
		chandlerypg.Config{DSN: "postgres://app:" + secret + "@db.example:5432/wishboard?password=" + secret, MaxOpenConns: 5})
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("log line quotes the password: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "db.example") {
		t.Fatalf("log line lost the host: %s", buf.String())
	}
}
