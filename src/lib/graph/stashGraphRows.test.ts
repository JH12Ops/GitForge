import { describe, expect, it } from 'vitest'
import {
  buildGraphRows,
  commitVirtualIndexByCommit,
  stashVirtualIndexByStash,
} from './stashGraphRows'

const commits = [{ sha: 'c' }, { sha: 'b' }, { sha: 'a' }]

describe('buildGraphRows', () => {
  it('pins the pending row to the top regardless of where HEAD sits', () => {
    expect(buildGraphRows(commits, 0, true, [])).toEqual([
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    // Newer commits from other refs (e.g. pulled remote tips) keep sorting
    // below WIP: it stays GitKraken-pinned at the very top.
    expect(buildGraphRows(commits, 0, false, [])).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })

  it('places every stash directly below the current branch row, in stash-index order', () => {
    const rows = buildGraphRows(commits, 0, true, [{ baseSha: 'c' }])
    expect(rows).toEqual([
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    const byCommit = commitVirtualIndexByCommit(rows)
    expect(byCommit.get(0)).toBe(1)
    expect(byCommit.get(1)).toBe(3)
    expect(stashVirtualIndexByStash(rows).get(0)).toBe(2)
  })

  it('keeps stashes below HEAD even when pinned without a pending row', () => {
    const rows = buildGraphRows(commits, 0, false, [{ baseSha: 'c' }])
    expect(rows).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })

  it('stacks multiple stashes below the HEAD row regardless of their bases', () => {
    const rows = buildGraphRows(commits, 0, false, [
      { baseSha: 'a' },
      { baseSha: 'b' },
      { baseSha: 'missing' },
    ])
    expect(rows).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'stash', stashIndex: 1 },
      { kind: 'stash', stashIndex: 2 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    const byCommit = commitVirtualIndexByCommit(rows)
    expect(byCommit.get(0)).toBe(0)
    expect(byCommit.get(1)).toBe(4)
    expect(byCommit.get(2)).toBe(5)
  })

  it('pins stashes below the branch row when HEAD is deeper in the list', () => {
    const rows = buildGraphRows(commits, 1, false, [{ baseSha: 'a' }])
    expect(rows).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })

  it('stacks stashes above the commits when HEAD is unknown', () => {
    const rows = buildGraphRows(commits, -1, false, [{ baseSha: 'c' }])
    expect(rows).toEqual([
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })
})
