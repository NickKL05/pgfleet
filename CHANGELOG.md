# Changelog

## Unreleased

### Dashboard

- New optional `pgfleet web` subcommand: a read-only web dashboard visualizing
  fleet migration status and schema drift, served from the single binary.
- Read-only JSON API (`/api/summary`, `/api/tenants`, `/api/drift`,
  `/api/drift/{tenant}`, `/api/versions`) reusing `migrate.Status`,
  `drift.Verify`, and `drift.Diff` behind a `Provider` interface; a short TTL
  cache (`--cache-ttl`) collapses a page load into one database pass.
- Vue 3 + Vite single-page UI (fleet overview with summary cards, tenants-per-
  version histogram, and a searchable/filterable table; per-tenant drift detail)
  embedded via `embed.FS`. A pre-built bundle ships in the repo so `go build`
  needs no Node toolchain.
- Multi-stage `Dockerfile` (Node build → Go build → distroless) and a
  `dashboard` Compose profile for a one-command deploy; `Makefile` targets for
  building the UI and binary. Handlers are covered by database-free tests.

## v0.1.0

First release. A single zero-cgo Go binary with two subsystems over a shared
core (config, discovery, connection pooling, reporting).

### Migration runner

- Versioned SQL migrations (`NNNN_description.up.sql` / `.down.sql`) applied
  across every tenant schema, with `migrate new`, `up`, `down`, and `status`.
- Per-tenant `_pgfleet_migrations` state table with SHA-256 checksums and
  checksum-mismatch detection (`--allow-dirty` to override).
- Bounded worker pool with per-tenant advisory locking, failure isolation
  (one tenant's failure never blocks others unless `fail_fast`), idempotent
  resume, dry run, the `-- pgfleet:no-transaction` escape hatch, and a glob
  `--tenants` selector for canary rollouts.
- Grouped human output and machine-readable JSON; exit codes 0/1/2/3.

### Drift detector

- Structural fingerprinting from a fixed set of database-wide catalog queries
  (never one per table), normalized so structurally identical tenants compare
  equal regardless of schema name, whitespace, or cast noise.
- `drift verify` (pass/fail), `drift diff` (object- and field-level
  explanations), `drift snapshot` (deterministic `schema.lock.json`), and
  `drift repair` (dependency-ordered corrective DDL).
- Repair refuses `DROP TABLE` / `DROP COLUMN` unless `--allow-destructive`, and
  `--apply` runs in a guarded transaction with the same lock and timeout rules
  as migrations.

### Quality

- Unit tests for normalization, fingerprinting, and diff/repair generation.
- testcontainers integration suite across Postgres 15, 16, and 17 covering the
  full spec test matrix, gated at 75% coverage on `internal/` in CI.
- CI: gofmt, go vet, golangci-lint v2, race-enabled unit tests, and a
  goreleaser snapshot build on every push.
