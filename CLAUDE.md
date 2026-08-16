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
  `respond.TypeForStatus`'s status→type mapping, and the "not actually a
  Postgres error" branch of `db/postgres`'s `UniqueViolation`/
  `ForeignKeyViolation` (the real-SQLSTATE branch needs a live connection —
  see below). Table-driven unit tests, no external dependencies.
  Millisecond-fast, always run.
- **Anything touching a real dependency** — `db` (bun's generic
  `Repository[T]`/`Model` against a real Postgres — the dialect itself
  doesn't matter to this package, Postgres is just the available backend to
  test against), `db/postgres` (its connection builder via `DB_URL` plus
  real unique/FK-violation SQLSTATEs), and `storage/s3` / `cache`'s real
  backends once those currently-empty stub packages grow implementations.
  Integration test against a testcontainer, behind `//go:build integration`,
  each test isolated in its own rolled-back transaction (`bun.Tx` satisfies
  `bun.IDB`) rather than sharing table state across parallel tests. Test the
  behavior you actually depend on (a real unique-constraint violation
  surfacing through `UniqueViolation`, `ConditionalUpdate` staying race-free
  under a guarded WHERE), not that bun/pgdriver work.
- **`respond/fiber` and `respond/nethttp`** — `httptest.NewRecorder`
  (nethttp) / Fiber's `app.Test` (fiber). Assert the JSON envelope
  (`{message, type, errors}` — this repo doesn't use problem+json) and
  status a consumer actually sees, including the `ErrorHandler`/`Wrap`
  error-mapping path: a `*respond.StatusError`, a native framework error,
  and an unmapped error each need to land on the right status/type and the
  5xx case needs to actually log.
- **Contracts** — `cache` and `storage` are each meant to grow multiple
  backends (memory/redis; local/s3) behind one interface. No tests on the
  interface itself once it's written; every backend instead runs a shared
  conformance suite (e.g. `cache.TestConformance(t, factory)`) so memory and
  redis are held to identical guarantees.
- **Architecture invariants** — `db`'s root package (generic `Repository[T]`/
  `Model`) must never import `db/postgres` or any future sibling driver
  package — that split is what makes `db` reusable across drivers instead of
  Postgres-only; `db/postgres` is the only place that imports
  `bun/driver/pgdriver` or a Postgres-specific error code. Same principle
  once `cache`/`storage` grow adapter subpackages (`memory`/`redis`,
  `local`/`s3`): the root package stays free of them, mirroring `respond`'s
  core (`respond.go`) staying free of any `fiber`/`net/http` import. Nothing
  currently enforces this mechanically (no import-linter test yet) — worth
  adding one if the driver list grows past two.

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
  fixed instant. No `time.Sleep` in tests.
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
