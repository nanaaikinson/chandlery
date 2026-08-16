package db

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/oklog/ulid/v2"
)

// IdentityModel embeds a ULID primary key, auto-populated on insert. Embed
// this (instead of Model) for tables with no created_at/updated_at columns.
type IdentityModel struct {
	ID string `bun:"id,pk" json:"id"`
}

// BeforeAppendModel assigns a new ULID to ID on insert if it isn't already
// set, mirroring Laravel's automatic key generation on model creation.
func (m *IdentityModel) BeforeAppendModel(_ context.Context, query bun.Query) error {
	if _, ok := query.(*bun.InsertQuery); ok && m.ID == "" {
		m.ID = ulid.Make().String()
	}
	return nil
}

// Model embeds a ULID primary key plus created_at/updated_at timestamps,
// auto-populated via BeforeAppendModel.
type Model struct {
	IdentityModel

	CreatedAt time.Time `bun:"created_at,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updated_at"`
}

// BeforeAppendModel delegates to IdentityModel for ID assignment (Go's
// method shadowing means it wouldn't otherwise run), stamps CreatedAt on
// insert without overwriting a caller-supplied value (e.g. for backfills),
// and refreshes UpdatedAt on every insert/update.
func (m *Model) BeforeAppendModel(ctx context.Context, query bun.Query) error {
	if err := m.IdentityModel.BeforeAppendModel(ctx, query); err != nil {
		return err
	}
	switch query.(type) {
	case *bun.InsertQuery:
		now := time.Now()
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now
		}
		m.UpdatedAt = now
	case *bun.UpdateQuery:
		m.UpdatedAt = time.Now()
	}
	return nil
}
