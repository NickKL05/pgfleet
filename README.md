# pgfleet

A multi-tenant PostgreSQL migration and drift toolkit for teams running
schema-per-tenant databases (50 to 5000+ schemas in a single database).

`pgfleet` is a single static Go binary with two subsystems that share one core:

- **Migration runner**: versioned SQL migrations applied across every tenant
  schema, with per-tenant state tracking, advisory locking, failure isolation,
  and a bounded worker pool.
- **Drift detector**: structural fingerprinting of schemas and (in progress)
  corrective DDL generation against a canonical reference.

## Status

This repository is under active development against a milestone plan. What works
today:

| Area | Command | State |
| --- | --- | --- |
| Migrations | `migrate new` | done |
| Migrations | `migrate up` | done |
| Migrations | `migrate down` | done |
| Migrations | `migrate status` | done |
| Drift | `drift verify` | done |
| Drift | `drift snapshot` | done |
| Drift | `drift diff` | planned (M4) |
| Drift | `drift repair` | planned (M5) |

See [docs/architecture.md](docs/architecture.md) for the design and
[the specification](pgfleet-spec.md) for the full requirements.

## Install

```
go build -o pgfleet ./cmd/pgfleet
```

The binary is zero-cgo and statically linkable across linux/amd64, linux/arm64,
darwin/arm64, and windows/amd64.

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
under two minutes:

```
docker compose up -d                         # Postgres seeded with 250 tenants
export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'
pgfleet migrate up                           # create users + index in all 250
pgfleet drift verify                         # clean: 250 tenants match the template
psql "$PGFLEET_DSN" -f demo/introduce_drift.sql   # drift 3 tenants on purpose
pgfleet drift verify                         # flags tenant_087, _142, _199 (exit 1)
```

## Development

```
go test ./...            # unit tests (no database required)
go vet ./...
gofmt -l .
```

Integration tests run the real catalog queries against a live PostgreSQL. They
are gated behind the `integration` build tag so the default `go test` stays fast
and offline:

```
docker compose up -d
PGFLEET_TEST_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet' \
  go test -tags integration ./internal/drift/...
```

The full testcontainers matrix across Postgres 15, 16, and 17 lands with the
remaining milestones.

## License

[MIT](LICENSE)
