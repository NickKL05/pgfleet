# Design decisions

Why the dashboard is built the way it is. Each entry is a decision that had a
real alternative, so the trade-off is worth stating.

## Same repository, new subcommand

The dashboard is `pgfleet web`, not a separate service.

Go's `internal/` visibility rule is the forcing function: a separate module
cannot import `internal/migrate` or `internal/drift`, so a standalone dashboard
would have had to either duplicate that logic or shell out to the CLI and parse
its output. Both options create a second source of truth about fleet state.

Keeping it in-repo also preserves the single-binary story that is the rest of
the tool's identity: one static binary, no runtime dependencies.

**Cost:** the repo now carries a Node toolchain and a frontend, so the project
is no longer purely Go. That is a real increase in surface area, mitigated by
the frontend being optional to build (see "Committed build artifact" below).

## Reuse `internal/`, never shell out

The handlers call the same functions the cobra commands call:

- `migrate.Status(ctx, pool, tenants, set, table, concurrency) → *report.RunReport`
- `drift.Verify(ctx, pool, tenants, ref, opts) → *report.DriftReport`
- `drift.Diff(ctx, pool, tenants, spec, opts) → *report.DriftReport`

These are already pure functions returning JSON-tagged structs, so most handlers
are a wrap-and-encode. The CLI and the dashboard therefore cannot disagree about
what "drifted" means: there is one implementation, and both surfaces are thin
wrappers over it.

## Read-only, deliberately

There are no migrate or repair buttons, no auth, and no write paths.

Partly scope control, but mostly framing: an observability dashboard is a
defensible thing to expose, whereas a web UI that mutates production databases
needs authentication, authorization, an audit trail, and CSRF protection before
it is honest to ship. Read-only means the threat model is "someone reads demo
data", which a deployment on a public IP can actually satisfy.

The destructive operations stay in the CLI, where they already have
confirmation prompts, dry-run modes, and `--allow-destructive` gates.

## A `Provider` interface between handlers and the database

The handlers depend on an interface, not on `*pgxpool.Pool`:

```go
type Provider interface {
    MigrationStatus(ctx context.Context) (*report.RunReport, error)
    DriftStatus(ctx context.Context) (*report.DriftReport, error)
    TenantDiff(ctx context.Context, schema string) (*report.DriftReport, error)
    LatestVersion() int
    Tenants() []string
}
```

The production implementation (`web.Fleet`) holds the pool, config, migration
set, and tenant list. Tests substitute a fake fleet.

The payoff is that every endpoint is covered by ordinary unit tests with no
database and no testcontainers: `go test ./internal/web/...` runs in seconds and
works offline. The database-facing code underneath is already covered by the
integration suite, so testing the handlers against a real Postgres would mostly
re-test code that is tested elsewhere.

## A short TTL cache, not a background refresher

Fleet-wide queries are memoized for `--cache-ttl` (default 3s).

The overview page mounts several components that each need fleet data; without
caching, one page load would issue `migrate status` and `drift verify` two or
three times each against 250 schemas. A background refresh loop was the
alternative, but it keeps querying a database nobody is looking at, and it
introduces staleness that is invisible to the user.

A TTL cache collapses a burst of near-simultaneous requests into one database
pass and then gets out of the way. `?refresh=1` (the UI's Refresh button)
bypasses it, so the user can always force a fresh read.

## Committed build artifact (`internal/web/dist`)

The compiled SPA is checked into the repository.

`//go:embed all:dist` requires the directory to exist at compile time, so
without a committed bundle every `go build`, `go test`, and `goreleaser` run
would first need Node installed. Committing the built assets keeps the Go build
self-contained: `go build ./...` works on a machine that has never seen npm.

**Cost:** a build artifact in version control, and the asset filenames are
content-hashed, so rebuilding the frontend produces a diff with renamed files.
The alternative (gitignore `dist/` and require a Node step before every Go
build) trades that churn for a much worse default developer experience.

`make web` and the Docker build regenerate the bundle from `web/` source.

## Absolute asset base (`base: '/'`)

Vite is configured with an absolute base rather than a relative one. This is not
cosmetic: with a relative base, a deep link like `/tenant/tenant_042` resolves
`./assets/app.js` against the route path, and the SPA fallback returns
`index.html` for the resulting `/tenant/assets/app.js`, so the browser gets HTML
where it expected JavaScript and renders nothing. See
[engineering-log.md](engineering-log.md).

## Multi-stage Docker to a distroless image

Stage 1 builds the SPA with Node, stage 2 compiles a `CGO_ENABLED=0` binary with
that bundle embedded, and the final stage copies the binary onto
`gcr.io/distroless/static`. Because the artifact is one static binary, the
runtime image needs no interpreter, package manager, or shell, which also means
there is no shell for an attacker to reach.
