import { describe, expect, it } from 'vitest'
import {
  buildGraphRows,
  commitVirtualIndexByCommit,
  stashVirtualIndexByStash,
} from './stashGraphRows'

const commits = [{ sha: 'c' }, { sha: 'b' }, { sha: 'a' }]

describe('buildGraphRows', () => {
  it('pins the pending row to the top regardless of where HEAD sits', () => {
    expect(buildGraphRows(commits, true, [])).toEqual([
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    // Newer commits from other refs (e.g. pulled remote tips) keep sorting
    // below WIP: it stays GitKraken-pinned at the very top.
    expect(buildGraphRows(commits, false, [])).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })

  it('places an anchored stash between the pending row and its base commit', () => {
    const rows = buildGraphRows(commits, true, [{ baseSha: 'c' }])
    expect(rows).toEqual([
      { kind: 'pending' },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    const byCommit = commitVirtualIndexByCommit(rows)
    expect(byCommit.get(0)).toBe(2)
    expect(stashVirtualIndexByStash(rows).get(0)).toBe(1)
  })

  it('inserts a stash immediately above its base commit', () => {
    const rows = buildGraphRows(commits, true, [{ baseSha: 'a' }])
    expect(rows).toEqual([
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'commit', commitIndex: 2 },
    ])
    const byCommit = commitVirtualIndexByCommit(rows)
    expect(byCommit.get(2)).toBe(4)
    expect(stashVirtualIndexByStash(rows).get(0)).toBe(3)
  })

  it('mounts unanchored stashes above the pending row', () => {
    expect(buildGraphRows(commits, true, [{ baseSha: 'missing' }])).toEqual([
      { kind: 'stash', stashIndex: 0 },
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    expect(buildGraphRows(commits, false, [{ baseSha: null }])[0]).toEqual({
      kind: 'stash',
      stashIndex: 0,
    })
  })

  it('keeps stash index order for stashes sharing a base', () => {
    const rows = buildGraphRows(commits, false, [
      { baseSha: 'b' },
      { baseSha: 'b' },
    ])
    expect(rows).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'stash', stashIndex: 1 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })
})
