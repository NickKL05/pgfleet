# Engineering log

Bugs and surprises from building the dashboard, with how each was found. These
are recorded because the debugging path is usually more interesting than the
fix.

## Deep links rendered a blank page

**Symptom.** The fleet overview at `/` worked. Navigating to `/tenant/tenant_142`
by clicking a row worked. Loading that same URL directly, or refreshing while on
it, produced a completely blank page with no console error.

**Cause.** Vite was configured with `base: './'`, so the built `index.html`
referenced its bundle as `./assets/index-<hash>.js`. At `/` that resolves to
`/assets/...` and is fine. At `/tenant/tenant_142` the browser resolves it
relative to the route path, requesting `/tenant/assets/index-<hash>.js`. That
path is not a real file, so the SPA fallback did what it is supposed to do and
returned `index.html`, with a `200` and `Content-Type: text/html`. The browser
refused to execute HTML as a module script, so nothing ever booted.

Confirmed with one request:

```
$ curl -s -o /dev/null -w "%{http_code} %{content_type}\n" \
    http://localhost:8080/tenant/assets/index-p-09XPdg.js
200 text/html; charset=utf-8
```

A `200 text/html` where JavaScript was expected is the whole bug in one line.

**Fix.** `base: '/'`. The binary always serves the SPA from the domain root, so
absolute asset paths are correct at any route depth.

**Why it hid for so long.** During early development the embedded bundle was a
single hand-written `index.html` with inline JavaScript and no external assets,
so there was nothing to resolve and nothing to break. The bug only appeared once a real Vite
build with external hashed assets was embedded. A single-file placeholder is not
a faithful stand-in for a bundler's output.

**Generalisable lesson.** An SPA fallback that returns `index.html` for
*everything* will happily mask missing-asset errors as blank pages. Returning
`404` for unmatched paths under `/assets/` would have surfaced this immediately.

## "Failed to fetch" as an error message

**Symptom.** After the demo backend was stopped, refreshing the dashboard showed
a red banner reading `Failed to load fleet: Failed to fetch`, and the page was
otherwise empty.

**Diagnosis.** Not a bug: the error path worked exactly as written. The problem
was that it was written badly:

1. `Failed to fetch` is the browser's internal wording for a network-level
   rejection. It tells a reader nothing about what to do.
2. The error state *replaced* the page, so a failed refresh discarded data that
   was already on screen and perfectly good.
3. There was no way to retry except reloading the whole page.

**Fix.** Classify the failure in the API client: a `fetch` rejection becomes
"Can't reach the pgfleet server" (stopped or still starting), while a 5xx
becomes "The server couldn't read the fleet", which is what a `502` from these
endpoints actually means, since the API returns `502` when the underlying fleet
query fails. The raw cause is kept as secondary detail rather than the headline.
The panel renders *above* the last good data instead of replacing it, and always
offers a Try again button.

**Verification.** Served the UI against a fake fleet, killed the backend
mid-session (error state appears, data preserved), restarted it, clicked Try
again (recovers). Done in both the overview and detail views.

## A tenant behind on migrations also reports as drifted

**Observation.** After rolling 20 tenants back to earlier versions to make the
demo show a mid-rollout fleet, the drifted count rose from 3 to 23.

**Why.** This is correct, and it is worth understanding rather than "fixing".
Drift is measured structurally against a reference schema (`tenant_template`),
not against migration bookkeeping. A tenant that has not applied migration 0002
genuinely lacks the index that migration creates, so its schema really does
differ from the reference. `migrate status` and `drift verify` answer two
different questions:

- **Migration status** asks *has this tenant applied every migration?* That is a
  bookkeeping question, answered from the `_pgfleet_migrations` state table.
- **Drift** asks *does this tenant's actual schema match the reference?* That is
  a structural question, answered from the catalog.

They usually agree, and the interesting cases are when they do not: a tenant
that is up to date on migrations but still drifted has been modified out of
band, which is exactly the failure mode the tool exists to catch. The three
deliberately drifted demo tenants are that case; the 20 rolled-back ones are
the ordinary case.

## The cache-bypass parameter was an amplification vector

**Observation.** Reviewing the deployment before publishing the URL, the
`?refresh=1` parameter stood out. It exists so the UI's Refresh button can force
a fresh read, and it does that by skipping the TTL cache entirely. The endpoint
is unauthenticated, so any client could skip the cache too, and every skipped
request is a full catalog scan across 250 tenant schemas. A cheap HTTP request
turning into expensive database work is the shape of an amplification attack,
and on a burstable `t3.micro` the practical damage is exhausted CPU credits and
an instance that stays throttled.

**What already limited it.** Two accidents of the design, worth naming because
they explain why this was a hardening task and not an incident: `fleetCache`
holds its mutex for the duration of the query, so concurrent refreshes serialize
and the database sees one scan at a time rather than fifty; and the pool is
capped at `concurrency + 2` connections with a 60s `statement_timeout`. The
failure mode was therefore queued requests and growing latency, not a database
meltdown.

**Fix.** A floor on how often a forced refresh may reach the database
(`--min-refresh`, default 1s). Within the floor, `?refresh=1` is served from
cache like any other request. The Refresh button stays responsive, because a
human clicking it waits longer than a second anyway, while a loop gets cached
responses.

Measured on the deployed instance: the first forced refresh took 109ms, the next
four took under 1ms each, and one sent after the floor elapsed took 130ms.

Added alongside it: a per-client token bucket on `/api/*` (static assets stay
unthrottled, so exhausting the API budget cannot stop the page loading), and
read/write/idle timeouts on the server. `WriteTimeout` spans handler execution,
so it is deliberately set above the database `statement_timeout` rather than
below it, or a slow but legitimate fleet query would be truncated.

**Lesson.** A cache is a performance feature until the cache-bypass is reachable
by anyone, at which point it is also a rate-limiting feature, and the bypass
needs its own limit.

## npm 11 silently skipped esbuild's install script

**Symptom.** `npm install` reported success and exited `0`, but printed a
warning that one package had install scripts "not yet covered by allowScripts",
naming `esbuild`.

**Why it matters.** esbuild's `postinstall` is what places the platform-specific
binary that Vite invokes. Without it, the install looks clean and the build
fails later for a reason that has nothing obviously to do with install scripts.

**Fix.** `npm rebuild esbuild` to run the skipped script, then build normally.

**Note for CI.** This is specific to npm 11+, which blocks lifecycle scripts by
default. CI and the Docker build both use Node 22 (npm 10), where the script
runs as it always did, so this never surfaces there. Worth knowing before
someone upgrades the CI image and hits it.

## Docker could not be tested on the development machine

For most of the build, `docker build` had never been run: the development
machine was non-admin with no WSL2, and Docker Desktop requires enabling
virtualisation features that need elevation.

Rather than claim the container worked, each stage was validated separately:

- the Node stage, by running `npm run build` and embedding the real output;
- the Go stage, by running its exact command
  (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w"`)
  and confirming a statically linked ELF binary with the bundle inside it
  (`grep` for the asset hash in the binary);
- the final stage, which only copies that binary onto distroless, was left as
  the one unverified step.

The first genuine `docker build` was the EC2 instance's first boot, and it
succeeded. The honest framing at the time was "every stage is verified except
the packaging", which is more useful than an unqualified "it works".
