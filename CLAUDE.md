## Testing

**Always write tests for code you add or change.** Absence of existing tests
in a package is not license to skip: set up the minimal harness (a
`_test.go` in the same package, a testcontainer for a live dependency)
rather than deferring. This is a library other services import — a bug in
`db.Repository[T]` or `respond`'s error mapping surfaces as a mystery in an
application that never touched this repo, so the usual "I'll catch it in
staging" safety net doesn't exist.

Match the test to the risk. This repo has no `contracts/`/`appctx/` layering
and no `config`/`clock`/`errs` packages — test against what's actually here:

- **Pure logic** — `env`'s `GetInt`/`GetBool`/`GetSlice`/`castString`
  coercions, `validator.SanitizeIssues`'s zog-issue-to-field grouping,
  `respond.TypeForStatus`'s status→type mapping, the "not actually a
  Postgres error" branch of `db/postgres`'s `UniqueViolation`/
  `ForeignKeyViolation` (the real-SQLSTATE branch needs a live connection —
  see below), `cache/memory` (an in-process map, not a real dependency —
  it runs `cache`'s shared conformance suite with a `WithClock` fake clock
  standing in for TTL expiry instead of sleeping), and `storage/local` (the
  local filesystem via a `t.TempDir()` root, sandboxed by `os.Root` — real
  I/O, but no external service, so it runs `storage`'s shared conformance
  suite same as any other fast test). Table-driven unit tests, no external
  dependencies. Millisecond-fast, always run.
- **Anything touching a real dependency** — `db` (bun's generic
  `Repository[T]`/`Model` against a real Postgres — the dialect itself
  doesn't matter to this package, Postgres is just the available backend to
  test against), `db/postgres` (its connection builder via `DB_URL` plus
  real unique/FK-violation SQLSTATEs), `cache/redis` (its `REDIS_URL`
  connection builder, plus the same conformance suite run against a real
  server — TTL expiry there means actually waiting out a short real ttl,
  since nothing can fast-forward Redis's own clock), and `storage/s3` (its
  `S3_ENDPOINT`/`S3_ACCESS_KEY_ID`/`S3_SECRET_ACCESS_KEY` connection
  builder, plus `storage`'s conformance suite run against a real MinIO
  container). Integration test against a testcontainer, behind
  `//go:build integration`, each `db` test isolated in its own rolled-back
  transaction (`bun.Tx` satisfies `bun.IDB`) rather than sharing table state
  across parallel tests, each `cache/redis` test isolated by giving every
  `Store` its own key prefix rather than sharing one keyspace, and each
  `storage/s3` test isolated the same way — every `Disk` its own key prefix
  within one shared bucket, rather than one bucket per test. Test the
  behavior you actually depend on (a real unique-constraint violation
  surfacing through `UniqueViolation`, `ConditionalUpdate` staying race-free
  under a guarded WHERE, `Flush`'s `SCAN` only ever touching its own prefix,
  `storage/local`'s sandbox actually rejecting a `..`-escaping path), not
  that bun/pgdriver/go-redis/minio-go work.
- **`respond/fiber` and `respond/nethttp`** — `httptest.NewRecorder`
  (nethttp) / Fiber's `app.Test` (fiber). Assert the JSON envelope
  (`{message, type, errors}` — this repo doesn't use problem+json) and
  status a consumer actually sees, including the `ErrorHandler`/`Wrap`
  error-mapping path: a `*respond.StatusError`, a native framework error,
  and an unmapped error each need to land on the right status/type and the
  5xx case needs to actually log.
- **Contracts** — `cache` (backends: `cache/memory`, `cache/redis`) and
  `storage` (backends: `storage/local`, `storage/s3`) each grow multiple
  backends behind one interface. No tests on the interface itself; every
  backend instead runs its package's shared conformance suite
  (`cache.TestConformance(t, factory)` plus `TestFlushRequiresPrefix`;
  `storage.TestConformance(t, factory)`) so, e.g., memory and redis — or
  local and s3 — are held to identical guarantees.
- **Architecture invariants** — `db`'s root package (generic `Repository[T]`/
  `Model`) must never import `db/postgres` or any future sibling driver
  package — that split is what makes `db` reusable across drivers instead of
  Postgres-only; `db/postgres` is the only place that imports
  `bun/driver/pgdriver` or a Postgres-specific error code. `cache` and
  `storage` follow the same split: neither root package (`Store`/`Disk`
  interface, sentinel errors, the conformance suite) imports its own
  backend subpackages — `cache/redis` is the only place that imports
  `go-redis`, `storage/s3` the only place that imports `minio-go`, mirroring
  `respond`'s core (`respond.go`) staying free of any `fiber`/`net/http`
  import. Nothing currently enforces this mechanically (no import-linter
  test yet) — worth adding one if either driver list grows past two.

Conventions:

- No `config.Map` wrapper exists here — `env` reads `os.Getenv` directly.
  Use `t.Setenv`, not a bare `os.Setenv`: it restores the variable after the
  test and Go fails the test outright if it also calls `t.Parallel()`, so
  env-var tests end up correctly serialized instead of silently racing.
- `t.Parallel()` by default otherwise. If a test can't be parallel, say why
  in a comment.
- Integration tests behind `//go:build integration`. `go test ./...` must
  stay fast and dependency-free; CI runs both tags.
- No `clock` package exists yet. If timing-sensitive logic lands (token
  expiry, TTLs), inject the current time via a parameter or func field
  rather than calling `time.Now()` inside the logic, so a test can supply a
  fixed instant. No `time.Sleep` in tests — except a real, external clock
  neither this repo nor the test controls (e.g. `cache/redis`'s TTL, which
  expires on Redis's own server-side clock): `cache/redis/redis_integration_test.go`'s
  `realAdvance` sleeps for real rather than faking it, since there's nothing
  else to fast-forward. If you find yourself reaching for `time.Sleep`
  anywhere else, that's very likely a sign the code under test should take
  its clock as a parameter instead.
- `db.Model`/`db.IdentityModel` only auto-assign a ULID via
  `BeforeAppendModel` when `ID` is unset — a test needing a deterministic ID
  just sets it explicitly before insert instead of asserting on a real
  `ulid.Make()` value.
- Assert on error kinds, never error strings: `errors.Is`,
  `errors.AsType[*respond.StatusError]`, the `ok` bool from
  `db/postgres`'s `UniqueViolation`/`ForeignKeyViolation`.
- Reach for a mock only when the real thing can't be containerized (no
  Postgres/MinIO/Redis available in CI). A fake Postgres that agrees with
  your assumptions about SQLSTATE codes tests your assumptions, not
  pgdriver's actual wire behavior.

<!-- code-review-graph MCP tools -->

## MCP Tools: code-review-graph

**IMPORTANT: This project has a knowledge graph. ALWAYS use the
code-review-graph MCP tools BEFORE using Grep/Glob/Read to explore
the codebase.** The graph is faster, cheaper (fewer tokens), and gives
you structural context (callers, dependents, test coverage) that file
scanning cannot.

### When to use graph tools FIRST

- **Exploring code**: `semantic_search_nodes` or `query_graph` instead of Grep
- **Understanding impact**: `get_impact_radius` instead of manually tracing imports
- **Code review**: `detect_changes` + `get_review_context` instead of reading entire files
- **Finding relationships**: `query_graph` with callers_of/callees_of/imports_of/tests_for
- **Architecture questions**: `get_architecture_overview` + `list_communities`

Fall back to Grep/Glob/Read **only** when the graph doesn't cover what you need.

### Key Tools

| Tool                        | Use when                                               |
| --------------------------- | ------------------------------------------------------ |
| `detect_changes`            | Reviewing code changes — gives risk-scored analysis    |
| `get_review_context`        | Need source snippets for review — token-efficient      |
| `get_impact_radius`         | Understanding blast radius of a change                 |
| `get_affected_flows`        | Finding which execution paths are impacted             |
| `query_graph`               | Tracing callers, callees, imports, tests, dependencies |
| `semantic_search_nodes`     | Finding functions/classes by name or keyword           |
| `get_architecture_overview` | Understanding high-level codebase structure            |
| `refactor_tool`             | Planning renames, finding dead code                    |

### Workflow

1. The graph auto-updates on file changes (via hooks).
2. Use `detect_changes` for code review.
3. Use `get_affected_flows` to understand impact.
4. Use `query_graph` pattern="tests_for" to check coverage.
