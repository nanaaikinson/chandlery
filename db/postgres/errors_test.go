package postgres_test

import (
	"errors"
	"testing"

	"github.com/nanaaikinson/chandlery/db/postgres"
)

// The success paths (a real 23503/23505 from Postgres) need a live
// connection to construct a genuine pgdriver.Error and live in
// errors_integration_test.go. This only covers the pure "not a Postgres
// error at all" branch, which needs no dependency.

func TestUniqueViolation_NonPostgresError(t *testing.T) {
	t.Parallel()

	if _, ok := postgres.UniqueViolation(errors.New("boom")); ok {
		t.Error("UniqueViolation(non-pg error) = true, want false")
	}
}

func TestForeignKeyViolation_NonPostgresError(t *testing.T) {
	t.Parallel()

	if _, ok := postgres.ForeignKeyViolation(errors.New("boom")); ok {
		t.Error("ForeignKeyViolation(non-pg error) = true, want false")
	}
}
