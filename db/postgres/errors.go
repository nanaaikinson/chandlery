package postgres

import (
	"errors"

	"github.com/uptrace/bun/driver/pgdriver"
)

// UniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), returning the violated constraint's name
// (Postgres wire protocol field 'n') when it is - useful when more than one
// unique constraint on a table can produce the same SQLSTATE, so a caller
// can tell which one actually fired instead of assuming.
func UniqueViolation(err error) (constraint string, ok bool) {
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || pgErr.Field('C') != "23505" {
		return "", false
	}
	return pgErr.Field('n'), true
}

// ForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (SQLSTATE 23503 - foreign_key_violation), returning the
// violated constraint's name - same shape as UniqueViolation. Postgres
// raises this same SQLSTATE both when a row can't be deleted because
// something else still references it (e.g. a product referenced by an
// existing order_items row via ON DELETE RESTRICT) and when an
// INSERT/UPDATE references a parent row that doesn't exist - there's no
// SQLSTATE-level way to tell those apart, so a caller distinguishes by
// which operation it just ran.
func ForeignKeyViolation(err error) (constraint string, ok bool) {
	var pgErr pgdriver.Error
	if !errors.As(err, &pgErr) || pgErr.Field('C') != "23503" {
		return "", false
	}
	return pgErr.Field('n'), true
}
