import { describe, expect, it } from 'vitest'
import {
  buildGraphRows,
  commitVirtualIndexByCommit,
  stashVirtualIndexByStash,
} from './stashGraphRows'

const commits = [{ sha: 'c' }, { sha: 'b' }, { sha: 'a' }]

describe('buildGraphRows', () => {
  it('anchors the pending row above the HEAD commit row', () => {
    expect(buildGraphRows(commits, true, [], 0)).toEqual([
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    // HEAD further down the list: pending anchors there, not at the top.
    expect(buildGraphRows(commits, true, [], 2)).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 2 },
    ])
    expect(buildGraphRows(commits, false, [], 0)).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })

  it('falls back to the top when HEAD is outside the loaded window', () => {
    expect(buildGraphRows(commits, true, [], -1)[0]).toEqual({ kind: 'pending' })
    expect(buildGraphRows(commits, true, [], 99)[0]).toEqual({ kind: 'pending' })
  })

  it('places the pending row below stashes anchored to the HEAD commit', () => {
    const rows = buildGraphRows(commits, true, [{ baseSha: 'c' }], 0)
    expect(rows).toEqual([
      { kind: 'stash', stashIndex: 0 },
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    const byCommit = commitVirtualIndexByCommit(rows)
    expect(byCommit.get(0)).toBe(2)
    expect(stashVirtualIndexByStash(rows).get(0)).toBe(0)
  })

  it('inserts a stash immediately above its base commit', () => {
    const rows = buildGraphRows(commits, true, [{ baseSha: 'a' }], 0)
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

  it('falls back to the top when the base commit is not loaded', () => {
    // HEAD is inside the window: the unanchored stash leads, pending anchors
    // at HEAD.
    expect(buildGraphRows(commits, true, [{ baseSha: 'missing' }], 0)).toEqual([
      { kind: 'stash', stashIndex: 0 },
      { kind: 'pending' },
      { kind: 'commit', commitIndex: 0 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
    expect(buildGraphRows(commits, false, [{ baseSha: null }], 0)[0]).toEqual({
      kind: 'stash',
      stashIndex: 0,
    })
  })

  it('keeps stash index order for stashes sharing a base', () => {
    const rows = buildGraphRows(commits, false, [
      { baseSha: 'b' },
      { baseSha: 'b' },
    ], 0)
    expect(rows).toEqual([
      { kind: 'commit', commitIndex: 0 },
      { kind: 'stash', stashIndex: 0 },
      { kind: 'stash', stashIndex: 1 },
      { kind: 'commit', commitIndex: 1 },
      { kind: 'commit', commitIndex: 2 },
    ])
  })
})
