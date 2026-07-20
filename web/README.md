# pgfleet dashboard (frontend)

Vue 3 + Vite single-page app for the read-only fleet dashboard. It is compiled
to static files and embedded into the `pgfleet` binary, so in production there
is no separate frontend to deploy.

## Develop

```
npm install
npm run dev          # serves on http://localhost:5173, proxies /api to :8080
```

Run `pgfleet web` in another terminal (against the demo compose stack) so the
proxied API has data.

## Build

```
npm run build        # writes the bundle to ../internal/web/dist
```

`go build` (or `make build`) then embeds that directory via `embed.FS`. The
repository ships a pre-built `internal/web/dist` so the Go binary builds with no
Node toolchain present; `npm run build` overwrites it with an optimized bundle.

## Layout

- `src/api.js` — thin fetch client for the `/api/*` endpoints.
- `src/router.js` — two routes: `/` (fleet overview) and `/tenant/:schema`.
- `src/views/` — the two pages.
- `src/components/` — summary cards, the searchable fleet table, the version
  histogram (Chart.js via vue-chartjs), and the status badge.
- `src/style.css` — the dark theme (all colors are CSS custom properties).
