# pgfleet: Multi-Tenant PostgreSQL Migration and Drift Toolkit

Working name: `pgfleet` (rename as desired). Single Go binary, two subsystems sharing one core: a fleet-wide migration runner and a schema drift detector/repairer. Target user: teams running schema-per-tenant PostgreSQL (50 to 5000+ schemas in one database).

---

## 1. Scope

### In scope
- Versioned SQL migrations applied across all tenant schemas in one database
- Structural fingerprinting of schemas and drift detection against a canonical reference
- Corrective DDL generation for drifted schemas
- CI-friendly output (JSON, exit codes)

### Out of scope (v1)
- Cross-database / sharded deployments
- Data migrations or backfills (structure only)
- Non-PostgreSQL engines
- GUI

---

## 2. Tech Stack Requirements

- Go 1.22+
- `github.com/jackc/pgx/v5` with `pgxpool` (no ORM, no database/sql)
- `github.com/spf13/cobra` for CLI, `spf13/viper` optional for config
- `golang.org/x/sync/errgroup` + semaphore for worker pools
- Testing: `testcontainers-go` spinning real Postgres 15/16/17
- Zero cgo, single static binary, goreleaser for builds (linux/amd64, linux/arm64, darwin/arm64, windows/amd64)
- Structured logging via `log/slog`, JSON or text handler selectable

---

## 3. Configuration

File `pgfleet.yaml` in repo root, all values overridable by flags and env vars (`PGFLEET_*`).

```yaml
database:
  # dsn read from env by default, never stored in file
  dsn_env: PGFLEET_DSN

tenants:
  # exactly one discovery mode required
  discovery:
    mode: query            # query | pattern | static
    query: "select schema_name from control.tenants where active"
    pattern: "tenant_%"    # used when mode: pattern (ILIKE against pg_namespace)
    static: []             # explicit list when mode: static
  exclude: ["tenant_template", "tenant_archive_%"]

migrations:
  dir: ./migrations
  table: _pgfleet_migrations   # created inside each tenant schema
  lock_id: 743201              # advisory lock namespace

drift:
  reference:
    mode: schema          # schema | snapshot
    schema: tenant_template
    snapshot: ./schema.lock.json
  ignore:
    - "table:_pgfleet_migrations"
    - "comment:*"

run:
  concurrency: 16
  statement_timeout: 60s
  lock_timeout: 5s
  fail_fast: false
```

### Config requirements
- R3.1: missing required keys produce a clear error naming the key, exit code 2
- R3.2: DSN must never appear in config file or logs (redact in all output)
- R3.3: `--config` flag overrides default path; flags override file; env overrides nothing (lowest of the three is file, then env, then flag)

---

## 4. Component A: Migration Runner

### 4.1 Migration file format
- Directory of plain SQL files: `NNNN_description.up.sql` and `NNNN_description.down.sql`
- `NNNN` is a zero-padded monotonically increasing integer; gaps allowed, duplicates are a hard error
- Files are executed as-is inside the tenant schema context; the tool sets `search_path` to `"<tenant>", public` per connection before execution
- A magic comment `-- pgfleet:no-transaction` on line 1 opts a migration out of transactional execution (needed for `CREATE INDEX CONCURRENTLY`)

### 4.2 State tracking
- Per-tenant table `_pgfleet_migrations(version int primary key, name text, checksum text, applied_at timestamptz, duration_ms int)`
- Checksum is SHA-256 of the normalized file bytes (LF line endings, trailing whitespace stripped)
- R4.1: if an applied migration's checksum differs from the file on disk, abort for that tenant with status `checksum-mismatch` (overridable with `--allow-dirty`, logged loudly)

### 4.3 Commands

```
pgfleet migrate up        [--to N] [--tenants glob] [--dry-run]
pgfleet migrate down      --to N   [--tenants glob] [--dry-run]   # down requires explicit target
pgfleet migrate status    [--tenants glob] [--json]
pgfleet migrate new <description>                                  # scaffolds up/down pair
```

### 4.4 Functional requirements
- R4.2: each tenant's pending migrations run inside a single transaction per migration (unless no-transaction flag), with `SET LOCAL statement_timeout` and `SET LOCAL lock_timeout` from config
- R4.3: per-tenant advisory lock (`pg_try_advisory_xact_lock(lock_id, hashtext(schema))`) prevents concurrent runners; a held lock yields status `locked`, not an error, and the tenant is retried once at end of run
- R4.4: worker pool of `run.concurrency` tenants in parallel; one tenant's failure never blocks others unless `fail_fast: true`
- R4.5: a failed migration rolls back that migration only; tenant is marked `failed` at version V and the run continues; final report lists every tenant at every version
- R4.6: `--dry-run` prints the exact SQL and target tenants without connecting in write mode (uses a read-only transaction to compute pending set)
- R4.7: re-running after partial failure is idempotent: completed tenants are skipped via state table, failed tenants resume at the failed migration
- R4.8: `migrate status` output groups tenants by (version, status) so 250 healthy tenants collapse to one line
- R4.9: `--tenants` accepts a glob matched against schema names after discovery (e.g. `--tenants 'tenant_1*'`) for canary rollouts

### 4.5 Output and exit codes
- Human output: aligned table, one summary line per (version, status) group, failures expanded with the Postgres error
- `--json`: machine-readable run report `{run_id, started_at, tenants: [{schema, from, to, status, error?}], summary}`
- Exit codes: 0 all succeeded, 1 any tenant failed, 2 config/usage error, 3 connection/discovery error

### 4.6 Performance requirements
- R4.10: 250 tenants, 1 pending migration of trivial DDL each, concurrency 16: complete in under 30 seconds on a local Postgres
- R4.11: connection count never exceeds `concurrency + 2` (pool cap enforced)
- R4.12: memory under 256 MB at 5000 tenants (stream results, no full preloading of file contents per worker; load each migration file once, share bytes)

---

## 5. Component B: Drift Detector

### 5.1 Canonical reference
Two modes:
- `schema`: a designated live template schema (e.g. `tenant_template`) is the source of truth
- `snapshot`: a committed `schema.lock.json` file produced by `pgfleet drift snapshot`, enabling drift checks in CI without a template schema

### 5.2 Fingerprinting model
- For each schema, build a normalized structural model from catalogs in a fixed number of set-based queries (no per-table queries; target: at most 8 catalog queries total for the entire database, each returning rows for all tenant schemas at once)
- Catalog sources: `pg_namespace`, `pg_class`, `pg_attribute`, `pg_attrdef`, `pg_constraint`, `pg_index`, `pg_type`, `pg_proc`, `pg_trigger`, `pg_policy`, `pg_sequences`, `information_schema.views`
- Object types covered in v1: tables, columns (name, type, nullability, default expression, generated, identity), primary keys, foreign keys, unique and check constraints, indexes (definition via `pg_get_indexdef` with schema name stripped), sequences (structure only, not current value), views (definition via `pg_get_viewdef`, normalized), triggers, functions in-schema (normalized body hash), RLS policies
- Normalization rules (hard requirements):
  - R5.1: strip the schema qualifier from all definitions before hashing so `tenant_42.users` and `tenant_template.users` compare equal
  - R5.2: default expressions compared via `pg_get_expr` output after whitespace and cast-noise normalization (`'x'::text` vs `'x'`)
  - R5.3: column order is significant by default; `--ignore-column-order` flag relaxes it
  - R5.4: ignore: sequence current values, table/index storage parameters unless `--strict`, comments, owner, ACLs
- Hashing: SHA-256 per object, objects sorted by (type, name) and hashed again into one schema fingerprint; per-object hashes retained for diffing

### 5.3 Commands

```
pgfleet drift verify   [--tenants glob] [--json]        # pass/fail per tenant
pgfleet drift diff     <tenant|--all> [--json]          # object-level differences
pgfleet drift repair   <tenant|--all> [--out dir] [--apply]
pgfleet drift snapshot [--out schema.lock.json]
```

### 5.4 Functional requirements
- R5.5: `verify` on a database with 250 identical schemas completes in under 5 seconds (set-based catalog reads, fingerprints computed in memory, parallel hashing)
- R5.6: `diff` output classifies every difference as one of: `missing` (object in reference, absent in tenant), `extra` (object in tenant only), `modified` (hash mismatch, with a field-level explanation for columns and constraints)
- R5.7: `repair` generates corrective DDL per tenant into one file per tenant (`repair/tenant_42.sql`), ordered dependency-safe (drop dependents first, create dependencies first); generation is always offline-safe and never connects in write mode without `--apply`
- R5.8: `--apply` requires an interactive confirmation showing tenant count and statement count; `--yes` bypasses for CI; every applied repair runs in a transaction with the same lock and timeout rules as migrations
- R5.9: repair must refuse to generate destructive statements (`DROP TABLE`, `DROP COLUMN`) unless `--allow-destructive` is set; without it, such drift is reported but skipped
- R5.10: ignore list from config supports `type:name` patterns with `*` wildcards
- R5.11: `snapshot` output is deterministic (stable ordering, no timestamps in hashed content) so it diffs cleanly in git
- R5.12: exit codes: 0 no drift, 1 drift found, 2 usage error, 3 connection error (verify is CI-gateable)

### 5.5 Known hard cases (must have explicit handling and tests)
- Generated columns and identity columns vs serial defaults
- Partial and expression indexes (compare normalized `pg_get_indexdef`)
- Foreign keys referencing other schemas (compare with target schema name preserved only when it is outside the tenant set)
- Domains and enums used by columns (enum label order matters; treat enum label set + order as part of the type hash)
- Collation and identical type aliases (`varchar` vs `character varying` must hash equal)

---

## 6. Shared Core Requirements

- R6.1: one discovery implementation shared by both components; discovery result cached per invocation
- R6.2: every command supports `--json` and `--tenants`
- R6.3: all destructive paths (down migrations, repair --apply) print a plan first and require confirmation or `--yes`
- R6.4: context cancellation (SIGINT) drains in-flight tenants gracefully: running transactions roll back, completed tenants stay completed, report still prints
- R6.5: read-only commands must be provably read-only: open transactions with `BEGIN READ ONLY`

---

## 7. Testing Requirements

- Unit: normalization functions table-driven with golden inputs (type aliases, default expression noise, index def stripping)
- Integration (testcontainers, Postgres 15, 16, 17 matrix):
  - T1: 50 schemas, apply 5 migrations, verify all at version 5
  - T2: kill one tenant mid-run (inject failing SQL), assert isolation, resume succeeds
  - T3: mutate one tenant (drop index, alter column type, add rogue table), `verify` flags exactly that tenant, `diff` names the exact objects, `repair` output applied to a copy converges to zero drift
  - T4: checksum mismatch detection
  - T5: concurrent runner contention via advisory locks
  - T6: 1000-schema synthetic database, verify under 15 s, migrate at concurrency 32 without pool exhaustion
- Property test: snapshot of a schema, restore into a fresh schema from generated DDL, fingerprints equal
- CI: GitHub Actions, race detector on, `golangci-lint`, coverage gate 75% on `internal/`

---

## 8. Repository Structure

```
pgfleet/
  cmd/pgfleet/main.go
  internal/
    config/        # load, validate, redact
    discovery/     # tenant enumeration
    migrate/       # runner, state table, locks
    drift/
      model/       # normalized structural model
      catalog/     # set-based catalog readers
      fingerprint/ # hashing
      diffgen/     # diff + ddl generation
    report/        # human + json renderers
    pgutil/        # pool, search_path, timeouts
  migrations/      # example
  testdata/
  docs/
```

---

## 9. Milestones

- M1 (week 1-2): config, discovery, `migrate new/up/status`, state table, single-tenant correctness
- M2 (week 3): worker pool, advisory locks, failure isolation, JSON report, dry run
- M3 (week 4-5): catalog readers + normalization + fingerprint, `drift verify` and `snapshot`
- M4 (week 6): `drift diff` with field-level explanations
- M5 (week 7-8): `drift repair` with dependency-ordered DDL and destructive-statement guard
- M6: docs, demo GIF (250-schema demo script included in repo), goreleaser, v0.1.0 tag

---

## 10. README Demo Script (portfolio requirement)

The repo must include `demo/seed.sql` creating 250 tenant schemas with deliberate drift in 3 of them, so any reviewer can run:

```
docker compose up -d
pgfleet migrate up
pgfleet drift verify        # flags tenant_087, tenant_142, tenant_199
pgfleet drift repair tenant_087 --out repair/
```

and see the value in under two minutes.
