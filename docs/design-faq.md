# Design FAQ

Common questions about how pgfleet works and why, answered from the code. Where
something is unmeasured or was deliberately left out, this says so rather than
implying otherwise.

## How does it fit together?

One static Go binary with two subsystems over a shared core (config, tenant
discovery, connection pooling, reporting):

- a **migration runner** that applies versioned SQL across every tenant schema
  with per-tenant state, advisory locking, and failure isolation;
- a **drift detector** that fingerprints each schema from the catalog and
  compares it to a canonical reference.

`pgfleet web` adds a read-only HTTP layer over those two subsystems and serves a
Vue 3 SPA that is compiled into the binary with `embed.FS`. The handlers call
the same functions the CLI commands call, so there is one implementation of
"what is this fleet's state" and two presentations of it.

## Why is the frontend inside the Go binary?

Deployment becomes copying one file. There is no static host, no CDN, no version
skew between an API and a separately deployed frontend, and the container image
is just the binary on a distroless base with no shell or package manager.

The cost is that the frontend must be built before the Go build, since
`//go:embed` needs the directory to exist at compile time. That is handled by
committing the built bundle so `go build` never requires Node, with `make web`
and the Docker build regenerating it.

## How is the HTTP layer tested without a database?

The handlers depend on a `Provider` interface rather than a pool, so tests pass
a fake fleet with canned reports. Every endpoint, the 404 path, the cache
behaviour, the rate limiter, and the SPA fallback are covered by ordinary unit
tests that need no Docker and run offline.

The database-facing code beneath that interface is covered separately by a
testcontainers integration suite across Postgres 15, 16, and 17, gated at 75%
coverage on `internal/`. Splitting it this way keeps the fast tests fast without
leaving the SQL untested.

## Why is the dashboard read-only?

A dashboard that mutates databases needs authentication, authorization, audit
logging, and CSRF protection before it is honest to ship. A read-only one needs
none of that, which is why it can sit on a public IP with generated demo data
behind it and the threat model stays "someone reads demo data".

The destructive operations exist (`drift repair --apply`, `migrate down`), but
they live in the CLI, where they already have confirmation prompts, dry-run
output, and an `--allow-destructive` gate for `DROP TABLE`/`DROP COLUMN`.

## How does it handle 250 tenants without hammering the database?

Two things. The drift subsystem issues a fixed number of set-based catalog
queries that each cover every schema at once, so reads scale with object count
rather than tenant count. And the HTTP layer memoizes the two fleet-wide reports
for a short TTL (default 3s), so a page load that mounts several data-hungry
components produces one database pass instead of one per component.

`?refresh=1` bypasses the cache, and because the endpoint is unauthenticated
that bypass has its own floor and a per-client rate limit in front of it. See
[the design decisions](design-decisions.md).

The dashboard's response time has not been benchmarked, so there is no latency
figure to quote here. The 250-tenant demo is comfortable to use, which is an
observation rather than a measurement.

## Which bug was hardest to track down?

Deep links rendering blank. Vite's relative asset base meant that on
`/tenant/tenant_142` the browser requested `/tenant/assets/app.js`, the SPA
fallback returned `index.html` with a `200`, and the browser silently refused to
execute HTML as a module script, giving no console error and just an empty page.

What made it interesting is that it was invisible until late: the placeholder
bundle used during development was a single file with inline JavaScript, so
there were no external assets to misresolve. The fix was one config line
(`base: '/'`), but the lesson was about the placeholder being an unfaithful
stand-in, and about an over-broad SPA fallback masking missing-asset errors as
blank pages. Full write-up in [the engineering log](engineering-log.md).

## How is it deployed?

A multi-stage Docker build (Node, then a static Go build, then
`gcr.io/distroless/static`) and a single small EC2 instance running
`docker compose`: the dashboard container plus a Postgres container seeded with
the 250-tenant demo fleet. The instance configures itself from a user-data
script on first boot.

No ECS, no load balancer, no RDS: for a demo those add cost and moving parts
without changing what the deployment demonstrates. ECS Fargate would be the
lightest step up if managed containers were a requirement.

## Known limitations

- **Filtering is client-side.** The fleet table ships all 250 rows and filters in
  the browser. That is the right call at this size and the wrong one at 5,000
  tenants, where it needs pagination and a query parameter.
- **The tenant list is resolved once at startup.** A tenant created afterwards is
  invisible until restart. Fine for a demo, wrong for an operational tool;
  re-discovery on a timer or on demand is the fix.
- **No authentication.** Deliberate for a read-only demo, and the first thing to
  add before pointing this at anything real.
- **No benchmarks for the dashboard path.** The drift subsystem has a performance
  target in the spec; the HTTP layer does not, and it should if anything came to
  depend on it.

## What this project does not demonstrate

- Any latency or throughput characteristics, since none were measured, and no
  behaviour beyond the 250-schema demo and the 1,000-schema scale test in the
  integration suite.
- Production operation. The fleet is generated demo data.
- A dashboard safe to expose to untrusted users over real data. It has no
  authentication, by design.
