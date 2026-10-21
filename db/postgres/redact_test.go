package postgres_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	chandlerypg "github.com/nanaaikinson/chandlery/db/postgres"
)

// A Config never shows its password, however it's printed: on its own or as
// a field of something larger, through fmt, slog's text and JSON handlers, or
// encoding/json.
func TestConfigNeverPrintsThePassword(t *testing.T) {
	t.Parallel()
	cfg := chandlerypg.Config{DSN: "postgres://app:" + secret + "@db.example:5432/wishboard?password=" + secret}
	type app struct{ Database chandlerypg.Config }

	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		outputs = append(outputs, fmt.Sprintf(verb, cfg), fmt.Sprintf(verb, app{cfg}))
	}
	for _, h := range []func(*bytes.Buffer) slog.Handler{
		func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(h(&buf)).Info("x", "config", cfg, "app", app{cfg})
		outputs = append(outputs, buf.String())
	}
	raw, err := json.Marshal(app{cfg})
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(raw))

	for _, out := range outputs {
		if strings.Contains(out, secret) {
			t.Errorf("printed the password: %s", out)
		}
		if !strings.Contains(out, "db.example") {
			t.Errorf("lost the host: %s", out)
		}
	}
}

// A password that isn't percent-encoded and holds a "/", "#" or "?" makes
// url.Parse put part of it in the host, path or fragment; such a DSN prints
// as unreadable rather than half-redacted.
func TestConfigRedactsDSNsURLParseMisreads(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{
		"postgres://app:1234/" + secret + "@db.example:5432/wishboard",
		"postgres://app:/" + secret + "@db.example:5432/wishboard",
		"postgres://app:" + secret + "#ab@db.example:5432/wishboard",
		"postgres://app:12#" + secret + "@db.example:5432/wishboard",
		"postgres://app:12?" + secret + "@db.example:5432/wishboard",
		"postgres://app:" + secret + "@db.example:5432/wishboard",
	} {
		out := fmt.Sprint(chandlerypg.Config{DSN: dsn})
		if strings.Contains(out, secret) || strings.Contains(out, "1234") {
			t.Errorf("Config{DSN: %q} printed %s", dsn, out)
		}
	}
	// A unix-socket DSN has no host but is perfectly readable.
	out := fmt.Sprint(chandlerypg.Config{DSN: "postgres://app:" + secret + "@/wishboard?host=/var/run/postgresql"})
	if strings.Contains(out, secret) || !strings.Contains(out, "/wishboard") {
		t.Errorf("unix-socket DSN printed %s", out)
	}
}

// A DSN that parses as a URL but that pgdriver rejects keeps pgdriver's
// reason, so a typo can be found, and still never the password.
func TestOpenKeepsTheDriversReason(t *testing.T) {
	t.Parallel()
	_, err := chandlerypg.Open(chandlerypg.Config{DSN: "postgres://app:" + secret + "@db.example:5432/wishboard?sslmode=requre"})
	if err == nil {
		t.Fatal("Open with sslmode=requre succeeded")
	}
	if !strings.Contains(err.Error(), "sslmode") || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v, want pgdriver's sslmode reason without the password", err)
	}
}

// Open runs the model registrars, as New always has.
func TestOpenRunsModelRegistrars(t *testing.T) {
	t.Parallel()
	ran := make(chan *bun.DB, 8)
	chandlerypg.RegisterModelFunc(func(db *bun.DB) {
		select {
		case ran <- db:
		default:
		}
	})
	db, err := chandlerypg.Open(chandlerypg.Config{DSN: "postgres://app@127.0.0.1:1/app"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for {
		select {
		case got := <-ran:
			if got == db {
				return
			}
		default:
			t.Fatal("Open didn't run the registered model func on its DB")
		}
	}
}
