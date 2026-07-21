# pgfleet architecture

pgfleet is one binary with two subsystems over a shared core. This document
describes the packages that exist today and how a run flows through them.

## Package layout

```
cmd/pgfleet/        CLI wiring (cobra): flags, exit codes, command handlers
internal/
  config/           load, validate, redact configuration
  discovery/        tenant enumeration and --tenants glob filtering
  migrate/          migration source parsing, state table, locks, runner
  pgutil/           pool construction, search_path, identifier quoting
  report/           human and JSON renderers over a shared run report
  drift/            catalog readers, fingerprint, diffgen, snapshot, repair
  web/              read-only dashboard: HTTP API + embedded Vue SPA
web/                Vue 3 + Vite frontend source (built into internal/web/dist)
```

## A migrate up run, end to end

1. **Config** (`internal/config`) is loaded and validated. A missing required
   key produces an error that names the key and maps to exit code 2. The DSN is
   resolved from the environment, never the file, and is redacted everywhere.

2. **Migration source** (`internal/migrate`) reads the migration directory once.
   Filenames are `NNNN_description.up.sql` / `.down.sql`. Bytes are normalized
   (LF endings, trailing whitespace stripped) and SHA-256 checksummed. Duplicate
   versions are a hard error; gaps are allowed. The bytes are loaded a single
   time and shared across all tenant workers to keep memory flat at high tenant
   counts.

3. **Pool** (`internal/pgutil`) is built with `MaxConns = concurrency + 2` so the
   live connection count is bounded. Each connection sets session
   `statement_timeout` and `lock_timeout` on connect.

4. **Discovery** (`internal/discovery`) enumerates tenants once per invocation
   (query, pattern, or static mode), applies the exclude list, then applies the
   optional `--tenants` glob for canary rollouts. The result is cached and shared
   by both subsystems.

5. **Runner** (`internal/migrate`) drives a bounded worker pool
   (`errgroup` + `SetLimit`). Each tenant runs on its own connection:
   - The state table `_pgfleet_migrations` is created if absent.
   - Applied checksums are compared to disk; a mismatch aborts that tenant with
     status `checksum-mismatch` unless `--allow-dirty`.
   - Each pending migration runs in its own transaction with a per-tenant
     transaction-scoped advisory lock and `SET LOCAL` timeouts. A held lock
     yields status `locked` (retried once at the end of the run), not an error.
   - A migration tagged `-- pgfleet:no-transaction` on line 1 runs outside a
     transaction under a session-level advisory lock instead, for statements
     like `CREATE INDEX CONCURRENTLY`.
   - A failed migration rolls back only itself; the tenant is marked `failed` at
     that version and the run continues. One tenant never blocks another unless
     `fail_fast` is set.

6. **Report** (`internal/report`) collects per-tenant results. Human output
   groups tenants by (version, status) so a healthy fleet collapses to one line,
   and expands failures with their Postgres error. `--json` emits the same data
   as `{run_id, started_at, tenants, summary}`.

## Isolation and safety properties

- Read-only paths (dry run) open `BEGIN READ ONLY` transactions, so they cannot
  write even by mistake.
- Destructive paths (`migrate down`) print a plan and require confirmation or
  `--yes`.
- Re-running after a partial failure is idempotent: completed tenants are
  skipped via the state table, failed tenants resume at the failed migration.

## The drift subsystem

`drift verify`, `drift diff`, `drift repair`, and `drift snapshot` are
implemented. The flow:

1. **Catalog read** (`internal/drift/catalog`) issues 8 set-based queries that
   each cover every tenant schema at once, never one query per table. Constraints
   and non-constraint indexes are merged into a single discriminated UNION to
   stay within the 8-query budget (spec 5.2). Reads scale with object count, not
   tenant count, which is what keeps verify under the 5s target at 250 schemas.

2. **Model assembly** (`internal/drift/catalog`) turns rows into one normalized
   `model.Schema` per tenant. Every definition has its owning schema qualifier
   stripped (so tenant_42.users and tenant_template.users compare equal),
   whitespace collapsed, textual cast noise removed (`'x'::text` equals `'x'`),
   and type names canonicalized (`varchar` equals `character varying`).

3. **Fingerprint** (`internal/drift/fingerprint`) flattens the model into
   `(type, name, body)` objects sorted deterministically, hashes each body with
   SHA-256, and folds the per-object hashes into one schema fingerprint. The
   per-object hashes are retained so `Diff` can classify each difference as
   missing, extra, or modified. Fingerprints are computed concurrently across
   schemas (`internal/drift`).

4. **Reference and comparison** (`internal/drift`) builds the canonical
   fingerprint from either a live template schema or a committed
   `schema.lock.json` snapshot, then diffs every tenant against it. The same
   model options (ignore list, column-order, strict) are applied to both sides.
   Snapshots are deterministic JSON with no timestamps so they diff cleanly in
   git.

5. **Field-level diff** (`internal/drift/diffgen`) backs `drift diff`. Where
   verify answers "did anything change", diffgen answers "what exactly
   changed": a modified column reports its type, nullability, default,
   generated, identity, and position changes; a modified constraint reports its
   kind and definition. Snapshot-mode diff falls back to object-level findings,
   since the hashes alone do not carry the reference's field values.

Object types covered: tables, columns (type, nullability, default, generated,
identity, position), primary/foreign/unique/check constraints, indexes, views,
sequences (structure only), functions (normalized body hash), triggers, RLS
policies, and enum types (label order significant).

6. **Repair** (`internal/drift/diffgen` plus `internal/drift/repair.go`) turns
   the diff into corrective DDL. Each difference becomes a CREATE/ADD (missing),
   DROP (extra), or ALTER (modified) statement. Whole-table additions and
   removals are handled as a unit so child objects are not emitted twice.
   Statements are ordered so dependents drop before dependencies and
   dependencies are created before dependents. `DROP TABLE` and `DROP COLUMN`
   are refused unless `--allow-destructive`; such drift is reported and skipped
   otherwise. Generation only reads the catalog; `--apply` executes each
   tenant's plan in one transaction under the same advisory lock and timeouts as
   a migration, after a confirmation prompt. Repair needs full object
   definitions, so it requires a schema-mode reference rather than a snapshot.

The end-to-end property holds (spec test T3): applying a generated repair to a
drifted tenant converges it to zero drift, verified against live PostgreSQL.

See sections 5 and 9 of [the specification](../pgfleet-spec.md).

## The dashboard (`internal/web`)

`pgfleet web` serves a read-only observability layer over the two subsystems. It
adds no new database logic: the HTTP handlers depend on a `Provider` interface
whose production implementation (`Fleet`) is a thin wrapper over the same
functions the CLI calls: `migrate.Status`, `drift.Verify`, and `drift.Diff`.
Because the reports (`report.RunReport`, `report.DriftReport`) are already
JSON-tagged, most handlers are a wrap-and-encode.

- **Startup** mirrors a CLI invocation: `loadApp` → `connect` → `tenants`, plus
  `migrate.Load`, run once in the `web` command; the resolved pool, config,
  migration set, and tenant list are handed to `web.NewFleet`.
- **Endpoints** are all `GET` and read-only: `/api/summary`, `/api/tenants`
  (migration status joined with drift per tenant), `/api/drift`,
  `/api/drift/{tenant}`, and `/api/versions`. A short TTL cache (`--cache-ttl`)
  memoizes the two fleet-wide queries so one page load hits the database once,
  not once per component; `?refresh=1` bypasses it.
- **The UI** is a Vue 3 + Vite single-page app (`web/`) built to static assets in
  `internal/web/dist` and served from an `embed.FS` with an index.html fallback
  for client-side routes. The whole dashboard therefore ships inside the single
  binary. The repository carries the pre-built bundle so `go build` needs no Node
  toolchain; `make web` or the Docker node stage regenerates it. The SPA is built
  with an absolute asset base (`base: '/'` in `web/vite.config.js`) so deep links
  like `/tenant/x` resolve `/assets/*` correctly on refresh or direct navigation.

Splitting the handlers behind the `Provider` interface keeps them testable
without a database: `go test ./internal/web/...` exercises every endpoint against
a fake fleet. The dashboard is strictly additive and read-only: it has no write
paths, no auth, and does not change any CLI behavior.
