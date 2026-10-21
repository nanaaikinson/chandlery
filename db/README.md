# db

Dialect-agnostic `bun` tooling — nothing here imports a specific driver:

- `Repository[T]` — a generic repository (`GetOne`, `GetMany`, `Count`,
  `Exists`, `Create`, `Update`, `UpdateByMap`, `Delete`) over any bun model.
  Works with `T` as a struct (`Repository[Model]`) or a pointer to one
  (`Repository[*Model]`); use the pointer form when you need a DB-assigned
  field (a generated ULID, a defaulted timestamp) to land back on your own
  variable after `Create`/`Update` — those methods take `item` by value, so
  with a non-pointer `T` the mutation only reaches the method's own copy.
- `ConditionalUpdate[M]` — a guarded `UPDATE ... WHERE` that reports whether
  a row actually matched, for flipping a row's state only if it's still in
  an expected prior one (avoids a race between a check and a write).
- `Model` / `IdentityModel` — embeddable base structs that assign a ULID
  primary key on insert (`Model` also stamps `created_at`/`updated_at`).

Anything driver-specific lives in its own subpackage instead:

- [`db/postgres`](postgres) — a `*bun.DB` connection builder and Postgres
  error classification (unique/foreign-key violation → constraint name).
  `New()` reads `DB_URL` and the `DB_*` pool variables itself; a service
  that loads its own configuration builds a `postgres.Config` and calls
  `Open(cfg)`, which returns an error (never a panic) whose text never
  contains the DSN's password. A `Config` never prints its password, through
  slog, fmt or JSON. pgdriver still fills what a DSN leaves out from
  `PGHOST`/`PGPORT`/`PGUSER`/`PGDATABASE`, so name all four in the DSN.

A future MySQL (or other) driver would get the same treatment: its own
`db/mysql` subpackage, `db`'s repository and models reused as-is.
