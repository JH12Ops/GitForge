/**
 * Row model for the commit graph list.
 *
 * Order (top to bottom): unanchored stashes, the uncommitted-changes (pending)
 * row pinned at the very top of the graph (GitKraken anchors WIP to the top so
 * later refs — pulled remote tips, other-branch commits — sort below it), then
 * commits newest-first with each stash inserted immediately above its base
 * commit.
 */

export type GraphRow =
  | { kind: 'pending' }
  | { kind: 'stash'; stashIndex: number }
  | { kind: 'commit'; commitIndex: number }

import { buildCommitIndex } from './commitIndex'

export function buildGraphRows(
  commits: { sha: string }[],
  hasPendingRow: boolean,
  stashes: { baseSha: string | null }[],
): GraphRow[] {
  const indexBySha = buildCommitIndex(commits)
  const byBase = new Map<number, number[]>()
  const unanchored: number[] = []
  for (let i = 0; i < stashes.length; i++) {
    const baseSha = stashes[i].baseSha
    const baseIndex = baseSha ? (indexBySha.get(baseSha) ?? -1) : -1
    if (baseIndex >= 0) {
      const list = byBase.get(baseIndex) ?? []
      list.push(i)
      byBase.set(baseIndex, list)
    } else {
      unanchored.push(i)
    }
  }

  const rows: GraphRow[] = []
  for (const stashIndex of unanchored) rows.push({ kind: 'stash', stashIndex })
  if (hasPendingRow) rows.push({ kind: 'pending' })
  for (let commitIndex = 0; commitIndex < commits.length; commitIndex++) {
    for (const stashIndex of byBase.get(commitIndex) ?? []) {
      rows.push({ kind: 'stash', stashIndex })
    }
    rows.push({ kind: 'commit', commitIndex })
  }
  return rows
}

/** Maps each commit index to its virtual row index. */
export function commitVirtualIndexByCommit(rows: GraphRow[]): Map<number, number> {
  const map = new Map<number, number>()
  rows.forEach((row, virtualIndex) => {
    if (row.kind === 'commit') map.set(row.commitIndex, virtualIndex)
  })
  return map
}

/** Maps each stash index to its virtual row index. */
export function stashVirtualIndexByStash(rows: GraphRow[]): Map<number, number> {
  const map = new Map<number, number>()
  rows.forEach((row, virtualIndex) => {
    if (row.kind === 'stash') map.set(row.stashIndex, virtualIndex)
  })
  return map
}
