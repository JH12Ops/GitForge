import type {
  ActionEnvelope,
  BlameRequest,
  BlameResult,
  CloneRepoRequest,
  CommitPageInfo,
  CommitPageRequest,
  ConflictRequest,
  ConflictResult,
  DiffRequest,
  DiffResult,
  DiscoveredRepo,
  DiscoverReposRequest,
  FileHistoryRequest,
  FileHistoryResult,
  ImagePreviewRequest,
  ImagePreviewResult,
  InitRepoRequest,
  OperationProgress,
  RecentRepository,
  RepoChangeEvent,
  SearchQuery,
  SearchResults,
  SnapshotOptions,
  SnapshotScope,
  WireWorkingTreeFile,
} from '@shared/git/models'
import type { GitErrorCode, SerializedGitError } from '@shared/git/errors'
import type { RebaseCommitsRequest, RebaseCommitsResult } from '@shared/git/rebase'
import type { ActionResult, GitAction, RepositoryData } from '@/types/git'
import type { GitService, SnapshotRefreshOptions } from '../git/GitService'
import { wireSnapshotToRepositoryData, reviveCommits, reviveWorkingTreeFile } from '../git/wireAdapter'
import { relayLog } from '@/lib/backend'
import { whenBackendRepoReady } from '@/lib/shell/backendGate'

/** Action pushed from the native (Go) menu over SSE `menu:action`. */
export interface MenuActionEvent {
  action: string
  url?: string
  path?: string
}

function envelopeToResult(envelope: ActionEnvelope): ActionResult {
  if (envelope.error) {
    const err = envelope.error
    const detail = err.detail ? `: ${err.detail}` : ''
    return { ok: false, message: `${err.message}${detail}`, errorCode: err.code, commands: envelope.commands }
  }
  return { ok: envelope.ok, message: envelope.message, commands: envelope.commands }
}

function toSnapshotOptions(
  scopes?: SnapshotScope[],
  options?: SnapshotRefreshOptions,
): SnapshotOptions | undefined {
  if (!scopes && !options) return undefined
  return { scopes, ...options }
}

/**
 * Commands that must not wait for the backend repo gate. They either never
 * touch the current repository, or they are the re-point itself — awaiting the
 * gate from inside `git_activate_repo` would deadlock on its own promise.
 * Mirrors the Tauri command list one-to-one since the Go backend serves the
 * same command names over HTTP.
 */
const REPO_GATE_EXEMPT = new Set([
  'git_activate_repo',
  'git_open_repo',
  'git_pick_and_open_repo',
  'git_clone_repo',
  'git_init_repo',
  'git_close_repo',
  'git_recent_repos',
  'git_remove_recent',
  'git_discover_repos',
  'git_reveal_path',
  'git_open_terminal_external',
  'git_cancel',
  'git_get_favorites',
  'git_set_favorites',
  // Builds a snapshot for a non-visible tab without touching `current`, so it
  // neither needs nor disturbs a pending re-point.
  'git_preload_repo',
  // Shell-parity routes: the native menu (menu bridge) and OS URL opener.
  'git_set_tabs',
  'git_open_external',
  // Cosmetic (theme → native window background) fires before any repo opens.
  'git_set_window_background',
])

const GATEWAY_PREFIX = '/gitforge/api'

type OpenRepoResponse = {
  ok: boolean
  snapshot?: Parameters<typeof wireSnapshotToRepositoryData>[0]
  error?: SerializedGitError
}

function basename(path: string): string {
  const parts = path.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || path
}

/**
 * Renderer-side recents list, kept in localStorage. The Go backend does not
 * implement recents persistence yet, so the browser-side store keeps the
 * welcome-screen flow working identically to the Tauri backend's.
 */
const RECENTS_KEY = 'gitforge:recent-repos'

function readRecents(): RecentRepository[] {
  try {
    const raw = localStorage.getItem(RECENTS_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as RecentRepository[]) : []
  } catch {
    return []
  }
}

function writeRecents(entries: RecentRepository[]): void {
  try {
    localStorage.setItem(RECENTS_KEY, JSON.stringify(entries))
  } catch {
    /* storage may be unavailable (private mode); recents are best-effort */
  }
}

function noteRecent(path: string): void {
  const cleaned = path.replace(/[\\/]+$/, '')
  const entries = readRecents().filter((r) => r.path !== cleaned)
  entries.unshift({ path: cleaned, name: basename(cleaned), lastOpened: Date.now() })
  writeRecents(entries)
}

/**
 * Renderer-side GitService backed by the Go process's HTTP JSON gateway
 * (`/gitforge/api/<command>`). The Vue layer stays backend-agnostic via the
 * `GitService` interface.
 */
export class WailsGitService implements GitService {
  private page: CommitPageInfo = { oldestSha: null, hasMore: false, total: null }
  private currentPath: string | null = null
  private readonly invoker: (cmd: string, body?: unknown) => Promise<unknown>

  constructor() {
    // A cached tab paints before its backend re-point settles. Every
    // repo-scoped command waits for `git_activate_repo` to answer, so a click
    // in that window (diff, blame, and destructive actions above all) can
    // never reach the repository the user just left.
    this.invoker = async (cmd, body) => {
      if (!REPO_GATE_EXEMPT.has(cmd)) await whenBackendRepoReady()
      return this.call(cmd, body)
    }
  }

  private async call<T>(cmd: string, body?: unknown): Promise<T> {
    const started = Date.now()
    try {
      const res = await fetch(this.callURL(cmd), {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body ?? {}),
      })
      if (!res.ok) {
        // Surface the gateway status; command-level domain errors arrive in
        // the response body as a 200 and are handled by each method.
        relayLog('gateway', `${cmd} failed (${res.status}) after ${Date.now() - started}ms`)
        throw new Error(`${cmd} failed (${res.status})`)
      }
      const data = (await res.json()) as T
      relayLog('gateway', `${cmd} ok in ${Date.now() - started}ms`)
      return data
    } catch (err) {
      // Network-level failure inside the webview: never swallow a hang — the
      // sink is how we see whether the request even left the renderer.
      relayLog('gateway', `${cmd} transport-error after ${Date.now() - started}ms: ${String(err)}`)
      throw err
    }
  }

  private callURL(cmd: string): string {
    return `${GATEWAY_PREFIX}/${cmd}`
  }

  get commitPage(): CommitPageInfo {
    return this.page
  }

  async recentRepos(): Promise<RecentRepository[]> {
    return readRecents()
  }

  async removeRecent(path: string): Promise<RecentRepository[]> {
    const remaining = readRecents().filter((r) => r.path !== path)
    writeRecents(remaining)
    return remaining
  }

  async openRepository(
    path: string,
    options?: SnapshotRefreshOptions,
  ): Promise<{ ok: boolean; data?: RepositoryData; message: string; errorCode?: GitErrorCode }> {
    const result = (await this.invoker('git_open_repo', { path, options })) as OpenRepoResponse
    return this.applyOpenedSnapshot(result)
  }

  private applyOpenedSnapshot(result: OpenRepoResponse): {
    ok: boolean
    data?: RepositoryData
    message: string
    errorCode?: GitErrorCode
  } {
    if (!result.ok || !result.snapshot) {
      return {
        ok: false,
        message: result.error?.message ?? 'Failed to open repository.',
        errorCode: result.error?.code,
      }
    }
    const data = wireSnapshotToRepositoryData(result.snapshot)
    // The snapshot carries the backend's canonical absolute path.
    this.currentPath = data.repository.path
    this.page = data.page
    noteRecent(data.repository.path)
    return { ok: true, data, message: `Opened ${data.repository.name}` }
  }

  async pickAndOpenRepository(
    options?: SnapshotRefreshOptions,
  ): Promise<{ ok: boolean; cancelled?: boolean; data?: RepositoryData; message: string; errorCode?: GitErrorCode }> {
    const result = (await this.invoker('git_pick_and_open_repo', { options })) as
      | { cancelled: true }
      | OpenRepoResponse
    if ('cancelled' in result) {
      return { ok: false, cancelled: true, message: '' }
    }
    // The Go side shows the native folder dialog, so the repository path only
    // exists on success — recents note it from the snapshot (in
    // applyOpenedSnapshot).
    return this.applyOpenedSnapshot(result)
  }

  async cloneRepository(
    request: CloneRepoRequest,
    options?: SnapshotRefreshOptions,
  ): Promise<{ ok: boolean; data?: RepositoryData; message: string; errorCode?: GitErrorCode }> {
    const result = (await this.invoker('git_clone_repo', { request, options })) as OpenRepoResponse
    return this.applyOpenedSnapshot(result)
  }

  async initRepository(
    request: InitRepoRequest,
    options?: SnapshotRefreshOptions,
  ): Promise<{ ok: boolean; data?: RepositoryData; message: string; errorCode?: GitErrorCode }> {
    const result = (await this.invoker('git_init_repo', { request, options })) as OpenRepoResponse
    return this.applyOpenedSnapshot(result)
  }

  async discoverRepositories(request: DiscoverReposRequest): Promise<DiscoveredRepo[]> {
    return (await this.invoker('git_discover_repos', { request })) as DiscoveredRepo[]
  }

  async closeRepository(): Promise<void> {
    await this.invoker('git_close_repo', { path: this.currentPath })
    this.page = { oldestSha: null, hasMore: false, total: null }
    this.currentPath = null
  }

  async activateRepository(
    path: string,
  ): Promise<{ ok: boolean; path?: string; message: string }> {
    const result = (await this.invoker('git_activate_repo', { path })) as {
      ok: boolean
      path?: string
      error?: SerializedGitError
    }
    if (!result.ok) {
      return {
        ok: false,
        path: result.path,
        message: result.error?.message ?? 'The repository could not be found.',
      }
    }
    this.currentPath = result.path ?? path
    return { ok: true, path: result.path, message: '' }
  }

  async preloadRepository(
    path: string,
    options?: SnapshotRefreshOptions,
  ): Promise<{
    ok: boolean
    data?: RepositoryData
    page?: CommitPageInfo
    message: string
    errorCode?: GitErrorCode
  }> {
    const result = (await this.invoker('git_preload_repo', { path, options })) as OpenRepoResponse
    if (!result.ok || !result.snapshot) {
      return {
        ok: false,
        message: result.error?.message ?? 'Failed to preload repository.',
        errorCode: result.error?.code,
      }
    }
    const data = wireSnapshotToRepositoryData(result.snapshot)
    // Deliberately leaves `this.currentPath`/`this.page` alone: they belong to
    // the repository the user is looking at, not the one warmed in background.
    return { ok: true, data, page: data.page, message: `Preloaded ${data.repository.name}` }
  }

  async refreshSnapshot(
    scopes?: SnapshotScope[],
    options?: SnapshotRefreshOptions,
  ): Promise<RepositoryData> {
    if (!this.currentPath) {
      // Matches the backend's own error for a repo-scoped call without a
      // current repository.
      throw new Error('No repository open')
    }
    const snapshot = (await this.invoker('git_snapshot', {
      path: this.currentPath,
      options: toSnapshotOptions(scopes, options),
    })) as Parameters<typeof wireSnapshotToRepositoryData>[0]
    const data = wireSnapshotToRepositoryData(snapshot)
    this.page = data.page
    return data
  }

  async loadMoreCommits(request: CommitPageRequest): Promise<{
    commits: RepositoryData['commits']
    page: CommitPageInfo
  }> {
    if (!this.currentPath) {
      // Nothing more to page for a repository that was never opened; an empty
      // page keeps the infinite-scroll path from looping.
      return { commits: [], page: { oldestSha: null, hasMore: false, total: null } }
    }
    const result = (await this.invoker('git_commit_page', {
      // The Go `CommitPageRequest` carries the repo path separately from the
      // Tauri variant, which relies on backend-side "current repo" state.
      request: { path: this.currentPath, ...request },
    })) as {
      commits: Parameters<typeof reviveCommits>[0]
      page: CommitPageInfo
    }
    this.page = result.page
    return { commits: reviveCommits(result.commits), page: result.page }
  }

  /** @deprecated Use openRepository / refreshSnapshot instead. */
  async load(): Promise<RepositoryData> {
    return this.refreshSnapshot()
  }

  async execute(action: GitAction): Promise<ActionResult> {
    const envelope = (await this.invoker('git_execute_action', {
      path: this.currentPath,
      action,
    })) as ActionEnvelope
    return envelopeToResult(envelope)
  }

  async getDiff(request: DiffRequest): Promise<DiffResult> {
    // `request.path` is a path INSIDE the repository; the repo itself travels
    // in the top-level body `path` field.
    return (await this.invoker('git_get_diff', {
      path: this.currentPath,
      request,
    })) as DiffResult
  }

  async getImagePreview(request: ImagePreviewRequest): Promise<ImagePreviewResult> {
    return (await this.invoker('git_get_image_preview', {
      path: this.currentPath,
      request,
    })) as ImagePreviewResult
  }

  async getBlame(request: BlameRequest): Promise<BlameResult> {
    return (await this.invoker('git_get_blame', {
      path: this.currentPath,
      request,
    })) as BlameResult
  }

  async getFileHistory(request: FileHistoryRequest): Promise<FileHistoryResult> {
    return (await this.invoker('git_get_file_history', {
      path: this.currentPath,
      request,
    })) as FileHistoryResult
  }

  async getConflict(request: ConflictRequest): Promise<ConflictResult> {
    return (await this.invoker('git_get_conflict', { path: this.currentPath, request })) as ConflictResult
  }

  async getCommitFiles(sha: string): Promise<RepositoryData['workingTree']> {
    const wire = (await this.invoker('git_get_commit_files', {
      path: this.currentPath,
      request: { sha },
    })) as WireWorkingTreeFile[]
    return wire.map(reviveWorkingTreeFile)
  }

  async getCompareFiles(fromSha: string, toSha: string): Promise<RepositoryData['workingTree']> {
    const wire = (await this.invoker('git_get_compare_files', {
      path: this.currentPath,
      request: { fromSha, toSha },
    })) as WireWorkingTreeFile[]
    return wire.map(reviveWorkingTreeFile)
  }

  async search(query: SearchQuery): Promise<SearchResults> {
    return (await this.invoker('git_search', { path: this.currentPath, request: query })) as SearchResults
  }

  async getRebaseCommits(request: RebaseCommitsRequest): Promise<RebaseCommitsResult> {
    return (await this.invoker('git_get_rebase_commits', {
      path: this.currentPath,
      request,
    })) as RebaseCommitsResult
  }

  async openTerminal(path?: string): Promise<boolean> {
    const result = (await this.invoker('git_open_terminal_external', {
      path: path ?? this.currentPath,
    })) as { ok: boolean }
    return result.ok
  }

  async revealPath(path: string): Promise<void> {
    await this.invoker('git_reveal_path', { path })
  }

  async cancelActiveOperation(): Promise<void> {
    await this.invoker('git_cancel', undefined)
  }

  /** Probes the gateway; also the transport behind `detectWailsBackend`. */
  async ping(value = 'ping'): Promise<string | null> {
    const res = await fetch(`${GATEWAY_PREFIX}/ping`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ value }),
    })
    if (!res.ok) return null
    try {
      const data: unknown = await res.json()
      return typeof data === 'string' ? data : null
    } catch {
      return null
    }
  }

  /**
   * The Go backend streams change notifications over a single SSE endpoint.
   * One shared EventSource serves all listeners (the browser caps concurrent
   * SSE connections per origin at six in HTTP/1.1), so multiple cbs must not
   * each open their own stream.
   */
  private static events: EventSource | null = null
  private static changedListeners = new Set<(event: RepoChangeEvent) => void>()
  private static progressListeners = new Set<(progress: OperationProgress) => void>()
  private static menuListeners = new Set<(action: MenuActionEvent) => void>()

  private static ensureEvents(): EventSource {
    if (this.events) return this.events
    const es = new EventSource('/gitforge/events')
    es.addEventListener('git:changed', (e) => {
      try {
        const event = JSON.parse((e as MessageEvent).data) as RepoChangeEvent
        for (const cb of this.changedListeners) cb(event)
      } catch {
        /* malformed payload — skip rather than break the stream */
      }
    })
    es.addEventListener('git:progress', (e) => {
      try {
        const progress = JSON.parse((e as MessageEvent).data) as OperationProgress
        for (const cb of this.progressListeners) cb(progress)
      } catch {
        /* malformed payload — skip rather than break the stream */
      }
    })
    es.addEventListener('menu:action', (e) => {
      try {
        const action = JSON.parse((e as MessageEvent).data) as MenuActionEvent
        for (const cb of this.menuListeners) cb(action)
      } catch {
        /* malformed payload — skip rather than break the stream */
      }
    })
    this.events = es
    return es
  }

  private static releaseEvents(): void {
    if (
      this.events &&
      this.changedListeners.size === 0 &&
      this.progressListeners.size === 0 &&
      this.menuListeners.size === 0
    ) {
      this.events.close()
      this.events = null
    }
  }

  onRepositoryChanged(cb: (event: RepoChangeEvent) => void): () => void {
    WailsGitService.ensureEvents()
    WailsGitService.changedListeners.add(cb)
    return () => {
      WailsGitService.changedListeners.delete(cb)
      WailsGitService.releaseEvents()
    }
  }

  onProgress(cb: (progress: OperationProgress) => void): () => void {
    WailsGitService.ensureEvents()
    WailsGitService.progressListeners.add(cb)
    return () => {
      WailsGitService.progressListeners.delete(cb)
      WailsGitService.releaseEvents()
    }
  }

  /** Native-menu actions pushed from the Go main process. */
  onMenuAction(cb: (action: MenuActionEvent) => void): () => void {
    WailsGitService.ensureEvents()
    WailsGitService.menuListeners.add(cb)
    return () => {
      WailsGitService.menuListeners.delete(cb)
      WailsGitService.releaseEvents()
    }
  }

  /** Mirror the renderer's repository-tab list into the native Tabs menu. */
  async setAppTabs(tabs: Array<{ path: string; name: string }>, activePath: string): Promise<void> {
    try {
      await this.invoker('git_set_tabs', { tabs, activePath })
    } catch (err) {
      // Non-fatal: a closed gateway just means no dynamic Tabs menu.
      relayLog('menu', `set-tabs failed: ${String(err)}`)
    }
  }
}

export const wailsGitService = new WailsGitService()
