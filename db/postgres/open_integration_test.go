//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	chandlerypg "github.com/nanaaikinson/chandlery/db/postgres"
)

// Open, given a Config built by the caller rather than read from the
// environment, reaches a real Postgres.
func TestOpenAndPing(t *testing.T) {
	t.Parallel()
	db, err := chandlerypg.Open(chandlerypg.Config{DSN: os.Getenv("DB_URL"), MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := chandlerypg.Ping(context.Background(), db); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	var one int
	if err := db.NewSelect().ColumnExpr("1").Scan(context.Background(), &one); err != nil || one != 1 {
		t.Fatalf("SELECT 1 = %d, %v", one, err)
	}
}
