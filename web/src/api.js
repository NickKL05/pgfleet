// Thin fetch wrapper over the read-only JSON API. Every call targets a relative
// /api path so the SPA works wherever the binary is mounted. Pass { refresh: true }
// to bypass the server-side cache (the "Refresh" button).

async function getJSON(path, { refresh = false } = {}) {
  const url = refresh ? `${path}${path.includes('?') ? '&' : '?'}refresh=1` : path
  const res = await fetch(url, { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let detail = res.statusText
    try {
      const body = await res.json()
      if (body && body.error) detail = body.error
    } catch {
      // non-JSON error body; keep the status text
    }
    throw new Error(`${res.status}: ${detail}`)
  }
  return res.json()
}

export const api = {
  summary: (opts) => getJSON('/api/summary', opts),
  tenants: (opts) => getJSON('/api/tenants', opts),
  drift: (opts) => getJSON('/api/drift', opts),
  tenantDrift: (schema, opts) => getJSON(`/api/drift/${encodeURIComponent(schema)}`, opts),
  versions: (opts) => getJSON('/api/versions', opts),
}
