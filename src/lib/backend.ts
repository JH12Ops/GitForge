export type NativeBackend = 'wails' | 'web'

declare global {
  interface Window {
    /** Set by the Go asset server when serving index.html (Wails mode). */
    __GITFORGE_BACKEND__?: 'wails'
  }
}

export function currentBackend(): NativeBackend {
  if (typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails') return 'wails'
  return 'web'
}

/**
 * Detects the Go (Wails) backend. The Go asset server stamps the served
 * index.html with `__GITFORGE_BACKEND__='wails'`, which is deterministic and
 * needs no networking; the HTTP probe stays as a belt-and-braces fallback.
 * (Vite's SPA fallback can answer unknown paths with 200 + HTML, so only a
 * JSON response from the gateway counts.)
 */
export async function detectWailsBackend(): Promise<boolean> {
  if (typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails') return true
  try {
    const res = await fetch('/gitforge/api/ping', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ value: 'detect' }),
      signal: AbortSignal.timeout(400),
    })
    return res.ok && (res.headers.get('content-type') ?? '').includes('json')
  } catch {
    return false
  }
}

/**
 * Relays renderer-side diagnostics (uncaught errors, failed gateway calls) to
 * the Go log sink, so release webviews (no devtools) stay diagnosable.
 */
export function relayLog(source: string, message: string): void {
  if (typeof window === 'undefined' || window.__GITFORGE_BACKEND__ !== 'wails') return
  try {
    void fetch('/gitforge/api/gitforge_log', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ src: source, msg: message }),
    }).catch(() => {})
  } catch {
    /* diagnostics are best-effort */
  }
}
