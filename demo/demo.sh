#!/usr/bin/env bash
#
# pgfleet demo: stand up 250 tenant schemas, migrate them, introduce drift in
# three, then detect, explain, and repair it. Run from the repo root:
#
#   ./demo/demo.sh
#
# Requires Docker and a Go toolchain. The Postgres container is left running so
# you can keep exploring; stop it with `docker compose down -v`.
set -euo pipefail

export PGFLEET_DSN='postgres://pgfleet:pgfleet@localhost:5432/fleet'

step() { printf '\n\033[1;36m== %s ==\033[0m\n' "$1"; }

step "build the single binary"
go build -o pgfleet ./cmd/pgfleet

step "start Postgres seeded with 250 tenants"
docker compose up -d
until docker compose exec -T postgres pg_isready -U pgfleet -d fleet >/dev/null 2>&1; do
  sleep 1
done

step "migrate up (creates users + index in all 250 tenants)"
./pgfleet migrate up

step "drift verify (clean: every tenant matches the template)"
./pgfleet drift verify

step "introduce deliberate drift in 3 tenants"
docker compose exec -T postgres psql -U pgfleet -d fleet -q -f - < demo/introduce_drift.sql

step "drift verify (flags tenant_087, tenant_142, tenant_199; exit 1)"
./pgfleet drift verify || true

step "drift diff tenant_142 (field-level explanation)"
./pgfleet drift diff tenant_142 || true

step "drift repair tenant_087 (writes corrective DDL)"
./pgfleet drift repair tenant_087 --out repair/ || true
echo "--- repair/tenant_087.sql ---"
cat repair/tenant_087.sql

step "done"
echo "Postgres is still running. Tear it down with: docker compose down -v"
