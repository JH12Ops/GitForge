import type { GitService } from './GitService'
import { WailsGitService } from '@/lib/wails/wailsGitService'
import { relayLog } from '@/lib/backend'

/**
 * Renderer-side Git backend: the Go (Wails) HTTP gateway. Swapped for the
 * in-memory mock in web-only dev (`VITE_USE_MOCK=true`).
 *
 * NOTE: kept as a synchronous `let` (live ESM binding) on purpose — the
 * previous top-level await blocked every importer and could leave `#app`
 * empty (black screen) when the module graph failed to evaluate.
 */
export let gitService: GitService = new WailsGitService()

/**
 * Mock activation, called once from `main.ts`. The real Go backend always
 * wins (the gateway serves the webview, so it is up by construction); in a
 * plain browser the mock activates when `VITE_USE_MOCK=true`. The return
 * value only reports mock usage.
 */
export async function useMockGitServiceIfRequested(): Promise<boolean> {
  if (typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails') return false
  relayLog('boot', 'backend selected: dev mock (gateway absent — browser dev)')
  if (!import.meta.env.DEV || import.meta.env.VITE_USE_MOCK !== 'true') return false
  const { MockGitService } = await import('./MockGitService')
  gitService = new MockGitService()
  return true
}
