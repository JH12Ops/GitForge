/**
 * Renderer ↔ native-window bridge (Go/Wails era).
 *
 * All native shell behaviour flows through the Go HTTP gateway:
 * - `git_open_external` validates the scheme and runs the OS opener,
 * - `git_set_window_background` repaints the native window to match the theme.
 *
 * In a plain browser (mock dev / docs preview) there is no gateway, so URL
 * opens fall back to window.open and background changes are no-ops (the CSS
 * owns the page there anyway).
 */

const GATEWAY_PREFIX = '/gitforge/api'

function isWailsWebview(): boolean {
  return typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails'
}

async function gatewayPost(cmd: string, body: unknown): Promise<boolean> {
  try {
    const res = await fetch(`${GATEWAY_PREFIX}/${cmd}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    return res.ok
  } catch {
    return false
  }
}

/** Open a URL in the system browser. */
export async function openExternalUrl(url: string): Promise<void> {
  if (typeof window === 'undefined') return
  if (await gatewayPost('git_open_external', { url })) return
  // Gateway unavailable (browser dev); a wails webview never falls back so a
  // dead gateway can't hijack the webview itself.
  if (!isWailsWebview()) window.open(url, '_blank', 'noopener')
}

/** Match the native window background to the theme (no-op without a bridge). */
export async function setWindowBackground(color: string): Promise<void> {
  if (typeof window === 'undefined' || !isWailsWebview()) return
  try {
    await gatewayPost('git_set_window_background', { color })
  } catch {
    /* gateway unavailable — cosmetic only */
  }
}
