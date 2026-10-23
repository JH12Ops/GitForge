/**
 * Pinned-branch persistence, backend-neutral.
 *
 * The Go (Wails) backend keeps favorites server-side over the HTTP gateway
 * (`git_get_favorites` / `git_set_favorites`). In a plain browser (mock dev)
 * there is no backend, so favorites degrade to localStorage.
 */

const WAILS_PREFIX = '/gitforge/api'
const LOCAL_KEY_PREFIX = 'gitforge:branch-favorites:'

function normalizeRepoPath(repoPath: string): string {
  return repoPath.replace(/[\\/]+$/, '')
}

async function gatewayFetch<T>(cmd: string, body: unknown): Promise<T> {
  const res = await fetch(`${WAILS_PREFIX}/${cmd}`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${cmd} failed (${res.status})`)
  return (await res.json()) as T
}

function readLocal(repo: string): string[] {
  try {
    const raw = localStorage.getItem(LOCAL_KEY_PREFIX + repo)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as string[]) : []
  } catch {
    return []
  }
}

function writeLocal(repo: string, names: string[]): void {
  try {
    localStorage.setItem(LOCAL_KEY_PREFIX + repo, JSON.stringify(names))
  } catch {
    /* storage may be unavailable (private mode); favorites are best-effort */
  }
}

/** Pinned-branch names for one repository. */
export async function loadBranchFavorites(repoPath: string): Promise<string[]> {
  const repo = normalizeRepoPath(repoPath)
  try {
    if (typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails') {
      const names = await gatewayFetch<string[]>('git_get_favorites', { request: { repoPath: repo } })
      return Array.isArray(names) ? names : []
    }
  } catch {
    /* fall through to local */
  }
  return readLocal(repo)
}

/** Persist pinned-branch names for one repository. */
export async function saveBranchFavorites(repoPath: string, names: string[]): Promise<string[]> {
  const repo = normalizeRepoPath(repoPath)
  try {
    if (typeof window !== 'undefined' && window.__GITFORGE_BACKEND__ === 'wails') {
      const result = await gatewayFetch<{ names: string[] }>('git_set_favorites', {
        request: { repoPath: repo, names },
      })
      return Array.isArray(result.names) ? result.names : names
    }
  } catch {
    /* fall through to local */
  }
  writeLocal(repo, names)
  return names
}
