// Thin fetch wrapper over the read-only JSON API. Every call targets a relative
// /api path so the SPA works wherever the binary is mounted. Pass { refresh: true }
// to bypass the server-side cache (the "Refresh" button).

// ApiError carries a short, human-readable headline plus a technical detail, so
// the UI can lead with plain language and keep the raw cause secondary.
export class ApiError extends Error {
  constructor(message, { detail = '', network = false } = {}) {
    super(message)
    this.name = 'ApiError'
    this.detail = detail
    this.network = network
  }
}

async function getJSON(path, { refresh = false } = {}) {
  const url = refresh ? `${path}${path.includes('?') ? '&' : '?'}refresh=1` : path

  let res
  try {
    res = await fetch(url, { headers: { Accept: 'application/json' } })
  } catch {
    // fetch only rejects on a network-level failure: the server is stopped,
    // still starting, or otherwise unreachable. The browser's own message
    // ("Failed to fetch") is not useful to a reader, so replace it.
    throw new ApiError('Can’t reach the pgfleet server', {
      detail: 'The server may still be starting up, or it is no longer running.',
      network: true,
    })
  }

  if (!res.ok) {
    let detail = res.statusText
    try {
      const body = await res.json()
      if (body && body.error) detail = body.error
    } catch {
      // non-JSON error body; keep the status text
    }
    // 502 is what the API returns when the fleet query itself failed, which in
    // practice means the database is unreachable or the query errored.
    const message =
      res.status >= 500
        ? 'The server couldn’t read the fleet'
        : `Request failed (${res.status})`
    throw new ApiError(message, { detail })
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
