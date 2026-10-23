import { addPerfSink, perfEnabled, type PerfEntry } from '@shared/perf'

/**
 * Dev bridge that gets perf samples onto the terminal running the Go backend.
 *
 * A wails (`WKWebView`) webview console is not piped to the terminal, so
 * `console.log` survives only in devtools. The sink forwards each sample to
 * the gateway's `gitforge_log` endpoint, which the backend prints to its log
 * sink — the same stream the rest of the runtime output uses.
 *
 * In a plain browser the devtools console is visible by itself, so the bridge
 * is a no-op there.
 */

const GATEWAY_PREFIX = '/gitforge/api'

/** Exported for tests: the production entry point is `attachPerfToTerminal`. */
export function createTerminalPerfSink(): (entry: PerfEntry) => void {
  return (entry) => {
    void fetch(`${GATEWAY_PREFIX}/gitforge_log`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ src: 'perf', msg: `${entry.label} ${entry.ms}ms` }),
    }).catch(() => {
      /* The bridge is diagnostics only: never let it break a switch. */
    })
  }
}

/** Wires perf samples to the terminal. Returns the unsubscribe function. */
export function attachPerfToTerminal(): () => void {
  if (!perfEnabled()) return () => {}
  if (typeof window === 'undefined' || window.__GITFORGE_BACKEND__ !== 'wails') return () => {}
  return addPerfSink(createTerminalPerfSink())
}
