# pgfleet

[![ci](https://github.com/NickKL05/pgfleet/actions/workflows/ci.yml/badge.svg)](https://github.com/NickKL05/pgfleet/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A multi-tenant PostgreSQL migration and drift toolkit for teams running
schema-per-tenant databases (50 to 5000+ schemas in a single database).

`pgfleet` is a single static Go binary with two subsystems that share one core:

- **Migration runner**: versioned SQL migrations applied across every tenant
  schema, with per-tenant state tracking, advisory locking, failure isolation,
  and a bounded worker pool.
- **Drift detector**: structural fingerprinting of schemas and corrective DDL
  generation against a canonical reference.

![pgfleet drift demo](demo/demo.svg)

...and an optional [read-only web dashboard](#dashboard-optional) over the same
fleet, served from that same single binary with the UI embedded in it.

**[Try the live demo](http://18.188.36.199:8080/)**: a small EC2 instance
running the 250-tenant demo fleet, mid-rollout, with three tenants deliberately
drifted. It is a throwaway demo box, so it may be torn down; the screenshots
below show the same thing.

![pgfleet dashboard: fleet overview](docs/images/dashboard-overview.png)

## TL;DR

**What it is.** A single Go binary that runs versioned SQL migrations across
every tenant schema in one PostgreSQL database (50 to 5000+ schemas), and
detects and repairs schema drift against a canonical reference. One config
file, one DSN from the environment, fleet-wide operations with per-tenant
isolation.

**Why it exists.** Schema-per-tenant databases drift: a hotfix applied to one
tenant by hand, a migration that half-failed, a tenant created before a column
existed. pgfleet makes the whole fleet converge to one canonical shape and
proves it did.

**See it work in 60 seconds** (needs Docker and a Go toolchain):

```
git clone https://github.com/NickKL05/pgfleet.git && cd pgfleet
./demo/demo.sh
```

The script seeds 250 tenant schemas, migrates them all, breaks three on
purpose, then detects, explains, and repairs the drift. Tear it down with
`docker compose down -v`.

**Use it on your own database:**

```
go build -o pgfleet ./cmd/pgfleet                     # build the binary
export PGFLEET_DSN='postgres://user:pass@host:5432/db'  # DSN lives in the env, never the file
# edit pgfleet.yaml: how to discover tenants + the reference schema

./pgfleet migrate status            # where is each tenant?
./pgfleet migrate up --dry-run      # print the exact SQL without applying
./pgfleet migrate up                # apply pending migrations fleet-wide
./pgfleet drift verify              # does every tenant match the reference?
./pgfleet drift diff tenant_42      # explain exactly what differs
./pgfleet drift repair tenant_42    # write corrective DDL (add --apply to run it)
```

**The vocabulary.** Migrations: `migrate new|up|down|status`. Drift:
`drift verify|diff|repair|snapshot`. Every command accepts `--json` for
machine-readable output and `--tenants '<glob>'` to scope a run (e.g. canary a
subset). Exit codes: `0` success, `1` a tenant failed or drift was found,
`2` config or usage error, `3` connection or discovery error.

**Safe by default.** Dry-run any change before applying, per-tenant advisory
locks, failure isolation (one tenant's failure never blocks the fleet),
checksum verification on every applied migration, and `drift repair` refuses to
emit `DROP TABLE` or `DROP COLUMN` unless you pass `--allow-destructive`.
Applying a repair runs in a guarded transaction behind a confirmation prompt
(`--yes` bypasses it for CI).

## Commands

| Area | Command | Purpose |
| --- | --- | --- |
| Migrations | `migrate new` | scaffold an up/down migration pair |
| Migrations | `migrate up` | apply pending migrations fleet-wide |
| Migrations | `migrate down` | roll back to an explicit version |
| Migrations | `migrate status` | show each tenant's version, grouped |
| Drift | `drift verify` | pass/fail each tenant against the reference |
| Drift | `drift diff` | object- and field-level differences |
| Drift | `drift repair` | generate or apply corrective DDL |
| Drift | `drift snapshot` | write a deterministic `schema.lock.json` |
| Dashboard | `web` | serve the read-only fleet dashboard (optional) |

See [docs/architecture.md](docs/architecture.md) for the design and
[the specification](pgfleet-spec.md) for the full requirements.

Written up in more depth in [docs/](docs/):
[design decisions](docs/design-decisions.md) (the trade-offs and what they
cost), an [engineering log](docs/engineering-log.md) (the bugs worth reading
about, and how they were found), and a [design FAQ](docs/design-faq.md)
(known limitations, and what is deliberately not measured).

## Install

Download a prebuilt binary for your platform from the
[releases page](https://github.com/NickKL05/pgfleet/releases), or build from
source:

```
go build -o pgfleet ./cmd/pgfleet
```

The binary is zero-cgo and statically linkable across linux/amd64, linux/arm64,
darwin/arm64, and windows/amd64. Releases are cut with
[goreleaser](https://goreleaser.com) on every `v*` tag.

## Configuration

`pgfleet` reads `pgfleet.yaml` from the working directory (override with
`--config`). The database DSN is never stored in the file; it is read from the
environment variable named by `database.dsn_env` and is redacted from all
output. Precedence is file, then env, then flag. See the annotated
[pgfleet.yaml](pgfleet.yaml) for every option.

```
export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'
```

## Quick start

```
pgfleet migrate new "create users table"   # scaffold an up/down pair
pgfleet migrate status                      # show each tenant's version, grouped
pgfleet migrate up --dry-run                # print the exact SQL without applying
pgfleet migrate up                          # apply pending migrations fleet-wide
pgfleet migrate up --tenants 'tenant_1*'    # canary a subset
```

Every command supports `--json` for machine-readable output and `--tenants` for
glob-scoped runs. Exit codes: `0` success, `1` a tenant failed, `2` config or
usage error, `3` connection or discovery error.

## Demo

The `demo/` directory seeds 250 tenant schemas so a reviewer can see the value in
under two minutes. The whole flow is scripted:

```
./demo/demo.sh
```

It starts a seeded Postgres, migrates all 250 tenants, breaks three of them on
purpose, then verifies, explains, and repairs the drift. See
[demo/README.md](demo/README.md) for details and how to record the GIF. The
individual steps:

```
docker compose up -d                         # Postgres seeded with 250 tenants
export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'
pgfleet migrate up                           # create users + index in all 250
pgfleet drift verify                         # clean: 250 tenants match the template
psql "$PGFLEET_DSN" -f demo/introduce_drift.sql   # drift 3 tenants on purpose
pgfleet drift verify                         # flags tenant_087, _142, _199 (exit 1)
pgfleet drift diff tenant_142                # explains the exact field change
pgfleet drift repair --all --out repair/     # write corrective DDL per tenant
pgfleet drift repair tenant_087 --apply      # apply the fix in a guarded transaction
```

Repair refuses to emit `DROP TABLE` or `DROP COLUMN` unless `--allow-destructive`
is set; such drift is reported and skipped otherwise. `--apply` runs each
tenant's fix in a single transaction with the same advisory lock and timeouts as
migrations, after a confirmation prompt (`--yes` bypasses it for CI).

## Dashboard (optional)

`pgfleet web` serves a read-only web dashboard that visualizes migration status
and schema drift across the fleet. The CLI stays the primary interface; the
dashboard is an observability layer over the same functions (`migrate status`,
`drift verify`/`diff`) and never mutates a database.

Live demo: **<http://18.188.36.199:8080/>** (demo instance; may be torn down).

```
export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'
pgfleet web --addr :8080          # then open http://localhost:8080
```

- **Fleet overview** (`/`): summary cards (total / on latest / behind / drifted),
  a tenants-per-version histogram, and a searchable, status-filterable table of
  every tenant with color-coded migration and drift badges. Rows link through to
  detail.
- **Tenant detail** (`/tenant/:schema`): the object- and field-level diff against
  the reference, grouped by object, or a clear "matches reference" state.

![pgfleet dashboard: tenant drift detail](docs/images/dashboard-tenant-detail.png)

The API is plain JSON and read-only, so it is also usable directly:

| Endpoint | Returns |
| --- | --- |
| `GET /api/summary` | headline counts + reference label |
| `GET /api/tenants` | per-tenant version, migration status, drift flag |
| `GET /api/drift` | `drift verify` across the fleet |
| `GET /api/drift/{tenant}` | object/field diff for one tenant |
| `GET /api/versions` | tenants-per-version histogram |

Fleet-wide queries are cached for a short window (`--cache-ttl`, default 3s) so a
page load does not hammer the database; append `?refresh=1` (or use the UI's
Refresh button) to force a fresh read.

### Architecture

The UI is a Vue 3 + Vite single-page app (source in [web/](web/)) compiled to
static files and embedded into the binary via `embed.FS`, so the whole dashboard
ships as part of the one static `pgfleet` binary, with no separate frontend to
deploy. The repository carries the pre-built `internal/web/dist` bundle, so
`go build` needs no Node toolchain; `make web` (or the Docker build) regenerates
it from the Vue source with `npm run build`.

### Run it against the demo fleet

```
docker compose up -d                                        # seeded Postgres (250 tenants)
export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'
pgfleet migrate up                                          # bring the fleet to latest
psql "$PGFLEET_DSN" -f demo/introduce_drift.sql             # break 3 tenants so drift shows red
pgfleet web                                                 # open http://localhost:8080
```

### Deploy with Docker

The [Dockerfile](Dockerfile) is a multi-stage build: a Node stage compiles the
Vue app, a Go stage builds the static binary with that UI embedded, and the final
image is just the binary on `gcr.io/distroless/static` (tiny, nonroot, no shell).
The DSN is passed at runtime via `PGFLEET_DSN` and never baked into the image.

```
docker build -t pgfleet .
docker run --rm -p 8080:8080 -e PGFLEET_DSN='postgres://user:pass@host:5432/db' pgfleet
```

Or bring the dashboard up alongside the seeded Postgres with Compose (the
`dashboard` profile keeps it out of the plain demo flow):

```
docker compose --profile dashboard up -d --build
```

### Deploy on AWS

A single small EC2 instance running the dashboard container plus a seeded
Postgres container is all this needs. No ECS, no load balancer, no RDS. The
instance configures itself from
[`deploy/ec2-user-data.sh`](deploy/ec2-user-data.sh): it installs Docker, builds
the image, seeds and migrates 250 tenants, drifts three of them, and serves the
dashboard on port 8080.

See **[docs/deploy-aws.md](docs/deploy-aws.md)** for the step-by-step console
walkthrough, security-group rules, troubleshooting, and teardown. Note the
dashboard has no authentication and is intended for demo data only.

See [docs/architecture.md](docs/architecture.md) for the dashboard's place in
the design.

## Development

```
go test ./...            # unit tests (no database required)
go vet ./...
gofmt -l .
```

The dashboard's Go handlers are covered by `go test ./internal/web/...` (they run
against a fake fleet, no database needed). To iterate on the frontend with hot
reload, run `pgfleet web` in one terminal and `npm run dev` in `web/` in another;
Vite proxies `/api` to `:8080`. See [web/README.md](web/README.md).

Integration tests exercise the real catalog, migration, and repair paths against
a live PostgreSQL started via testcontainers. They are gated behind the
`integration` build tag so the default `go test` stays fast and offline. CI runs
them across the Postgres 15, 16, and 17 matrix and enforces a 75% coverage gate
on `internal/` (the database-facing code is only exercised here):

```
# requires Docker; testcontainers pulls the image automatically
go test -tags integration -coverpkg=./internal/... -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
```

Set `PGFLEET_PG_IMAGE` (e.g. `postgres:16`) to pick the image, or
`PGFLEET_TEST_DSN` to run against an already-running database instead of
starting a container. The suite covers the spec test matrix: 50-schema apply
(T1), failure isolation and resume (T2), drift detect/explain/repair (T3),
checksum mismatch (T4), advisory-lock contention (T5), a 1000-schema scale run
(T6), and the snapshot-restore property test.

## License

[MIT](LICENSE)
