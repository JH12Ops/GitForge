import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { GitAction } from '@shared/git/actions'
import type { SnapshotScope } from '@shared/git/models'
import type { RepositoryData } from '@/types/git'

const fakeBackend = vi.hoisted(() => ({
  executeCalls: [] as GitAction[],
  executeImpl: null as null | ((action: GitAction) => Promise<{ ok: boolean; message: string }>),
  refreshScopes: [] as Array<SnapshotScope[] | undefined>,
}))

vi.mock('@/lib/git', () => ({
  gitService: {
    get commitPage() {
      return { oldestSha: null, hasMore: false, total: null }
    },
    recentRepos: async () => [],
    onRepositoryChanged: () => () => {},
    onProgress: () => () => {},
    onWindowFocused: () => () => {},
    execute: (action: GitAction) => {
      fakeBackend.executeCalls.push(action)
      if (!fakeBackend.executeImpl) throw new Error('no execute impl set')
      return fakeBackend.executeImpl(action)
    },
    refreshSnapshot: async (scopes?: SnapshotScope[]) => {
      fakeBackend.refreshScopes.push(scopes)
      return {
        repository: { name: 'repo', path: '/repo' },
        commits: [],
        branches: [],
        tags: [],
        stashes: [],
        worktrees: [],
        workingTree: [],
        authors: [],
        commitAuthor: null,
      } as unknown as RepositoryData
    },
  },
}))

import { useRepositoryStore } from '@/stores/repository'
import { useUiStore } from '@/stores/ui'

function openFakeRepo(): void {
  const repo = useRepositoryStore()
  repo.repository = { name: 'repo', path: '/repo' } as unknown as RepositoryData['repository']
}

/** Full-snapshot refreshes have `undefined` scopes; scoped ones carry scopes. */
function fullRefreshCount(): number {
  return fakeBackend.refreshScopes.filter((scopes) => scopes === undefined).length
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  // `requestChangesCheck`/`runStatusPoll` keep module-level timers and a
  // dedupe stamp: clear them so fake-timer time restarts don't leak.
  useRepositoryStore().resetBackgroundChecks()
  fakeBackend.executeCalls = []
  fakeBackend.refreshScopes = []
  fakeBackend.executeImpl = async () => ({ ok: true, message: 'Fetched from all remotes' })
})

describe('window-focus change checks', () => {
  it('coalesces rapid focus notifications into one check (full refresh + fetch)', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()
    useUiStore().setAutoFetchIntervalSec(0)

    repo.requestChangesCheck()
    repo.requestChangesCheck()
    repo.requestChangesCheck()

    // Still debouncing; nothing has fired.
    expect(fakeBackend.refreshScopes).toHaveLength(0)

    await vi.advanceTimersByTimeAsync(200)
    expect(fullRefreshCount()).toBe(1)
    // One silent auto-fetch tick accompanies the snapshot refresh, even
    // while periodic auto-fetch is disabled: focusing is the user asking
    // for a remote AND local check.
    expect(fakeBackend.executeCalls).toEqual([{ kind: 'fetch' }])
    // Silent: no toast, no operation banner, no cancel button.
    expect(repo.operation).toBeNull()
    expect(repo.operationPhase).toBeNull()
    expect(repo.canCancel).toBe(false)
  })

  it('dedupes a second activation inside the dedupe window', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()
    useUiStore().setAutoFetchIntervalSec(0)

    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(200)
    expect(fullRefreshCount()).toBe(1)

    // A repeat focus right after the check (e.g. the backend `git:focus`
    // event landing after the DOM one) is swallowed, not re-fetched.
    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(200)
    expect(fullRefreshCount()).toBe(1)

    // Beyond the dedupe window the next activation checks again.
    await vi.advanceTimersByTimeAsync(1_100)
    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(200)
    expect(fullRefreshCount()).toBe(2)
  })

  it('never runs while a manual operation or a tab switch owns the store', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()
    useUiStore().setAutoFetchIntervalSec(0)

    repo.busyAction = 'push'
    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(200)
    expect(fakeBackend.refreshScopes).toHaveLength(0)
    expect(fakeBackend.executeCalls).toHaveLength(0)

    repo.busyAction = null
    // The skipped check stamped its dedupe window: the next check must
    // wait it out.
    await vi.advanceTimersByTimeAsync(1_100)
    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(200)
    expect(fullRefreshCount()).toBe(1)
  })

  it('stays silent with no repository open', async () => {
    const repo = useRepositoryStore()
    repo.requestChangesCheck()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(fakeBackend.refreshScopes).toHaveLength(0)
    expect(fakeBackend.executeCalls).toHaveLength(0)
  })
})

describe('periodic status poll (worktree edits)', () => {
  it('refreshes status on a fixed cadence', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()

    repo.restartStatusPoll()
    expect(fakeBackend.refreshScopes).toHaveLength(0)

    await vi.advanceTimersByTimeAsync(15_000)
    expect(fakeBackend.refreshScopes).toEqual([['status']])

    await vi.advanceTimersByTimeAsync(15_000)
    expect(fakeBackend.refreshScopes).toEqual([['status'], ['status']])
  })

  it('skips ticks while a manual operation owns the store', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()

    repo.restartStatusPoll()
    repo.busyAction = 'rebase'
    await vi.advanceTimersByTimeAsync(30_000)
    expect(fakeBackend.refreshScopes).toHaveLength(0)

    repo.busyAction = null
    await vi.advanceTimersByTimeAsync(15_000)
    expect(fakeBackend.refreshScopes).toEqual([['status']])
  })

  it('stays stopped with no repo open and stops on demand', async () => {
    const repo = useRepositoryStore()
    repo.restartStatusPoll()
    await vi.advanceTimersByTimeAsync(600_000)
    expect(fakeBackend.refreshScopes).toHaveLength(0)

    openFakeRepo()
    repo.restartStatusPoll()
    repo.stopStatusPoll()
    await vi.advanceTimersByTimeAsync(600_000)
    expect(fakeBackend.refreshScopes).toHaveLength(0)
  })

  it('reschedules even when a tick throws', async () => {
    const repo = useRepositoryStore()
    openFakeRepo()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    // First tick rejects (async error), second must still be scheduled.
    vi.spyOn(repo, 'refreshSnapshot').mockRejectedValueOnce(new Error('snapshot exploded'))

    repo.restartStatusPoll()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(fakeBackend.refreshScopes).toEqual([['status']])
    vi.restoreAllMocks()
  })
})
