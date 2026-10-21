//go:build integration

package postgres_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	chandlerypg "github.com/nanaaikinson/chandlery/db/postgres"
)

// Config.Debug installs bundebug's query hook, which writes each failed
// query to stderr. Not parallel: it swaps os.Stderr.
func TestOpenDebugLogsQueries(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	db, err := chandlerypg.Open(chandlerypg.Config{DSN: os.Getenv("DB_URL"), Debug: true})
	os.Stderr = prev
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var one int
	if err := db.NewSelect().ColumnExpr("4242").TableExpr("no_such_table_debug_probe").Scan(context.Background(), &one); err == nil {
		t.Fatal("query against a missing table succeeded")
	}
	w.Close()
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "4242") {
		t.Fatalf("debug output %q doesn't show the failed query", out)
	}
}
